package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// blockedWordMsg is shown to a sender whose message contains a blocked word.
const blockedWordMsg = "Please stop: your message contains words that are not allowed here, so it was not sent. Remove %q and try again only if your enquiry is genuine."

// normalizeForMatch lowercases s and turns every run of characters that are
// not letters or digits into one space, padded at both ends, so a blocked
// word or phrase matches only on whole words ("cialis" never hits "specialist").
func normalizeForMatch(s string) string {
	var b strings.Builder
	b.WriteByte(' ')
	space := true
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	if !space {
		b.WriteByte(' ')
	}
	return b.String()
}

// matchBlockedWord returns the first word in words found in any of texts.
func matchBlockedWord(words []string, texts ...string) (string, bool) {
	hay := normalizeForMatch(strings.Join(texts, " \n "))
	for _, w := range words {
		n := normalizeForMatch(w)
		if strings.TrimSpace(n) == "" {
			continue
		}
		if strings.Contains(hay, n) {
			return w, true
		}
	}
	return "", false
}

// checkBlockedWords reports the blocked word found in the submission, if any,
// and records the hit so the owner can see which words are doing the work.
func (s *Server) checkBlockedWords(ctx context.Context, texts ...string) (string, error) {
	rows, err := s.DB.Query(ctx, `SELECT word FROM contact_blocked_words ORDER BY length(word) DESC`)
	if err != nil {
		return "", err
	}
	words, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	w, ok := matchBlockedWord(words, texts...)
	if !ok {
		return "", nil
	}
	if _, err := s.DB.Exec(ctx, `UPDATE contact_blocked_words SET hits = hits + 1, last_hit_at = now() WHERE lower(word) = lower($1)`, w); err != nil {
		s.Log.Error("blocked word hit", "err", err)
	}
	return w, nil
}

func (s *Server) listBlockedWords(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id, word, hits, last_hit_at, created_by, created_at FROM contact_blocked_words ORDER BY lower(word)`)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, word, by string
		var hits int
		var last *time.Time
		var at time.Time
		if err := rows.Scan(&id, &word, &hits, &last, &by, &at); err != nil {
			s.internal(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "word": word, "hits": hits, "last_hit_at": last, "created_by": by, "created_at": at})
	}
	if err := rows.Err(); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) addBlockedWord(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Word string `json:"word"`
	}
	if !decode(w, r, &in) {
		return
	}
	word := strings.Join(strings.Fields(in.Word), " ")
	if strings.TrimSpace(normalizeForMatch(word)) == "" || len(word) > 100 {
		writeError(w, 400, "enter a word or phrase of up to 100 characters with at least one letter or digit")
		return
	}
	var id string
	err := s.DB.QueryRow(r.Context(), `INSERT INTO contact_blocked_words (word, created_by) VALUES ($1, $2) RETURNING id`, word, actor(r)).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		writeError(w, 409, "that word is already blocked")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "contact.blocked_word.add", "contact_blocked_word", id, map[string]any{"word": word})
	writeJSON(w, 201, map[string]any{"id": id, "word": word})
}

func (s *Server) deleteBlockedWord(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var word string
	err := s.DB.QueryRow(r.Context(), `DELETE FROM contact_blocked_words WHERE id::text = $1 RETURNING word`, id).Scan(&word)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "contact.blocked_word.delete", "contact_blocked_word", id, map[string]any{"word": word})
	w.WriteHeader(http.StatusNoContent)
}
