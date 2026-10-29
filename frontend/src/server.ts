import {
  AngularNodeAppEngine,
  createNodeRequestHandler,
  isMainModule,
  writeResponseToNodeResponse,
} from '@angular/ssr/node';
import express, { type NextFunction, type Request, type Response } from 'express';
import { createHash, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import type { ReadableStream as NodeReadableStream } from 'node:stream/web';

const browserDistFolder = join(import.meta.dirname, '../browser');
const env = process.env;
/** The Go API. Browsers only ever talk to this server, which forwards API paths. */
const apiOrigin = (env['API_INTERNAL_URL'] || 'http://localhost:8080').replace(/\/+$/, '');

const app = express();
app.disable('x-powered-by');
const angularApp = new AngularNodeAppEngine();

// Content-Security-Policy. Scripts come only from this origin plus the Google
// services that load after consent (Tag Manager / Analytics) or on the owner's
// sign-in page (Firebase Auth). Inline <script type="application/json|ld+json">
// blocks are data, not script, so they need no exception.
const authDomain = env['FIREBASE_AUTH_DOMAIN'] ? `https://${env['FIREBASE_AUTH_DOMAIN']}` : 'https://*.firebaseapp.com';
const emulator = env['FIREBASE_AUTH_EMULATOR_URL'] ?? '';
// The build inlines a small, constant event-dispatch script into index.html.
// Allow exactly that content by hash; per-request scripts carry the nonce.
const inlineHashes = (() => {
  try {
    const html = readFileSync(join(import.meta.dirname, 'index.server.html'), 'utf8');
    return [...html.matchAll(/<script(?![^>]*\bsrc=)(?![^>]*type="application\/(?:ld\+)?json")[^>]*>([\s\S]*?)<\/script>/g)]
      .map((m) => `'sha256-${createHash('sha256').update(m[1]).digest('base64')}'`)
      .join(' ');
  } catch {
    return '';
  }
})();
const csp = [
  "default-src 'self'",
  "base-uri 'self'",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "form-action 'self'",
  `script-src 'self' 'nonce-__NONCE__' ${inlineHashes} https://www.googletagmanager.com https://apis.google.com`,
  "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
  "font-src 'self' https://fonts.gstatic.com data:",
  "img-src 'self' data: blob: https:",
  "media-src 'self' https:",
  `connect-src 'self' https://*.google-analytics.com https://*.analytics.google.com https://www.googletagmanager.com https://identitytoolkit.googleapis.com https://securetoken.googleapis.com${emulator ? ' ' + emulator : ''}`,
  `frame-src https://www.youtube-nocookie.com ${authDomain} https://accounts.google.com${emulator ? ' ' + emulator : ''}`,
  'upgrade-insecure-requests',
].join('; ');

app.use((req, res, next) => {
  const nonce = randomBytes(16).toString('base64');
  res.locals['nonce'] = nonce;
  const policy = env['NODE_ENV'] === 'production' ? csp : csp.replace('; upgrade-insecure-requests', '');
  res.setHeader('Content-Security-Policy', policy.replace('__NONCE__', nonce));
  res.setHeader('X-Content-Type-Options', 'nosniff');
  res.setHeader('Referrer-Policy', 'strict-origin-when-cross-origin');
  res.setHeader('Permissions-Policy', 'camera=(), microphone=(), geolocation=(), interest-cohort=()');
  res.setHeader('Cross-Origin-Opener-Policy', 'same-origin-allow-popups');
  if (env['NODE_ENV'] === 'production') res.setHeader('Strict-Transport-Security', 'max-age=63072000; includeSubDomains; preload');
  if (req.path === '/admin' || req.path.startsWith('/admin/')) {
    res.setHeader('X-Robots-Tag', 'noindex, nofollow');
    res.setHeader('Cache-Control', 'no-store');
  }
  next();
});

app.get('/healthz', (_req, res) => {
  res.json({ status: 'ok' });
});

/** Public Firebase web config for the owner sign-in page. These values are not secrets. */
app.get('/app-config.json', (_req, res) => {
  res.setHeader('Cache-Control', 'no-store');
  res.json({
    firebase: { apiKey: env['FIREBASE_API_KEY'] ?? '', authDomain: env['FIREBASE_AUTH_DOMAIN'] ?? '', projectId: env['FIREBASE_PROJECT_ID'] ?? '' },
    authEmulatorUrl: emulator,
  });
});

// --- API proxy ---------------------------------------------------------------

const hopByHop = new Set(['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer',
  'transfer-encoding', 'upgrade', 'host', 'content-length', 'content-encoding', 'expect',
  // Set by this server only; never forward a client's own value.
  'x-client-ip', 'x-serverless-authorization']);

/**
 * The visitor's IP, for the API's rate limits and consent records.
 * TRUSTED_PROXY_HOPS counts the load balancers in front of this server
 * (1 behind Cloud Run or Container Apps ingress, 0 when exposed directly).
 */
function clientIp(req: Request): string {
  if (env['TRUST_CLOUDFLARE_IP'] === 'true' && typeof req.headers['cf-connecting-ip'] === 'string') {
    return req.headers['cf-connecting-ip'];
  }
  const hops = Number(env['TRUSTED_PROXY_HOPS'] ?? 0) || 0;
  const xff = String(req.headers['x-forwarded-for'] ?? '').split(',').map((s) => s.trim()).filter(Boolean);
  const chain = [...xff, (req.socket.remoteAddress ?? '').replace(/^::ffff:/, '')];
  return chain[Math.max(0, chain.length - 1 - hops)] ?? '';
}

// On Cloud Run the API only accepts calls with a Google identity token for the
// web service account. It goes in X-Serverless-Authorization so the owner's
// Firebase token can keep using Authorization.
const idTokenAudience = env['API_ID_TOKEN_AUDIENCE'] ?? '';
let idToken = { value: '', expires: 0 };
async function serverlessAuth(): Promise<string> {
  if (!idTokenAudience) return '';
  if (Date.now() < idToken.expires) return idToken.value;
  const r = await fetch('http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=' +
    encodeURIComponent(idTokenAudience), { headers: { 'Metadata-Flavor': 'Google' }, signal: AbortSignal.timeout(5000) });
  if (!r.ok) throw new Error('metadata identity token: HTTP ' + r.status);
  idToken = { value: await r.text(), expires: Date.now() + 45 * 60_000 };
  return idToken.value;
}

async function proxy(req: Request, res: Response, next: NextFunction) {
  const headers = new Headers();
  for (const [k, v] of Object.entries(req.headers)) {
    if (v === undefined || hopByHop.has(k)) continue;
    headers.set(k, Array.isArray(v) ? v.join(', ') : v);
  }
  // Append the peer address so the API can count it as one trusted hop.
  const peer = req.socket.remoteAddress ?? '';
  const xff = req.headers['x-forwarded-for'];
  headers.set('x-forwarded-for', [xff, peer].filter(Boolean).join(', '));
  headers.set('x-forwarded-host', req.headers.host ?? '');
  headers.set('x-forwarded-proto', (req.headers['x-forwarded-proto'] as string) || req.protocol);
  headers.set('accept-encoding', 'identity');
  const ip = clientIp(req);
  if (ip) headers.set('x-client-ip', ip);
  if (req.headers['content-length']) headers.set('content-length', req.headers['content-length']);

  const hasBody = !['GET', 'HEAD'].includes(req.method);
  try {
    const token = await serverlessAuth();
    if (token) headers.set('x-serverless-authorization', 'Bearer ' + token);
    const upstream = await fetch(apiOrigin + req.originalUrl, {
      method: req.method,
      headers,
      body: hasBody ? (Readable.toWeb(req) as unknown as BodyInit) : undefined,
      redirect: 'manual',
      signal: AbortSignal.timeout(120_000),
      // Required by Node's fetch when streaming a request body.
      ...(hasBody ? { duplex: 'half' } : {}),
    } as RequestInit);
    res.status(upstream.status);
    upstream.headers.forEach((v, k) => {
      if (!hopByHop.has(k) && k !== 'set-cookie') res.setHeader(k, v);
    });
    const cookies = upstream.headers.getSetCookie();
    if (cookies.length) res.setHeader('set-cookie', cookies);
    if (!upstream.body) {
      res.end();
      return;
    }
    Readable.fromWeb(upstream.body as unknown as NodeReadableStream).on('error', next).pipe(res);
  } catch (err) {
    console.error('api proxy error', req.method, req.path, (err as Error).message);
    if (!res.headersSent) res.status(502).json({ error: 'The service is temporarily unavailable. Please try again.' });
    else res.end();
  }
}

app.use(['/api', '/mcp', '/oauth', '/.well-known/oauth-authorization-server', '/.well-known/oauth-protected-resource'], proxy);
app.get(['/sitemap.xml', '/robots.txt', '/llms.txt'], proxy);

// --- static files and SSR ------------------------------------------------------

app.use(
  express.static(browserDistFolder, {
    maxAge: '1y',
    index: false,
    redirect: false,
    setHeaders: (res, path) => {
      // Hashed bundles can be cached forever; everything else revalidates daily.
      if (!/-[A-Z0-9]{8,}\.(js|css)$/.test(path)) res.setHeader('Cache-Control', 'public, max-age=86400');
    },
  }),
);

// Uploaded media lives in a private bucket behind the API. Files baked into
// the image (public/media) are served by the static handler above first.
app.use('/media', (req, res, next) => (req.method === 'GET' || req.method === 'HEAD' ? proxy(req, res, next) : next()));

app.use((req, res, next) => {
  angularApp
    .handle(req, { nonce: res.locals['nonce'] })
    .then((response) => {
      if (!response) return next();
      if (!response.headers.has('cache-control') && !/^\/(admin|subscribe|unsubscribe)(\/|$)/.test(req.path)) {
        response.headers.set('cache-control', 'public, max-age=0, s-maxage=300, stale-while-revalidate=600');
      }
      return writeResponseToNodeResponse(response, res);
    })
    .catch(next);
});

if (isMainModule(import.meta.url) || process.env['pm_id']) {
  const port = process.env['PORT'] || 4000;
  app.listen(port, (error) => {
    if (error) {
      throw error;
    }
    console.log(`Web server listening on http://localhost:${port}, API at ${apiOrigin}`);
  });
}

/** Request handler used by the Angular CLI (dev-server and build). */
export const reqHandler = createNodeRequestHandler(app);
