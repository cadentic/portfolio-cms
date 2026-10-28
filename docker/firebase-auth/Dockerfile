# Firebase Auth emulator for local sign-in. Uses a "demo-" project id, so no
# real Firebase project or credentials are needed.
FROM node:24-alpine
RUN apk add --no-cache openjdk21-jre-headless && npm install -g firebase-tools@14 && npm cache clean --force
WORKDIR /srv
COPY firebase.json start.sh ./
EXPOSE 9099 4400
CMD ["./start.sh"]
