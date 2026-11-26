package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const listPageSize = 50

func listPage(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("page")
	if raw == "" {
		return 1, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 100000 {
		return 0, fmt.Errorf("página inválida")
	}
	return n, nil
}
func listURL(r *http.Request, page int) string {
	q := url.Values{}
	for _, key := range []string{"sortField", "q", "history"} {
		if v := r.URL.Query().Get(key); v != "" {
			q.Set(key, v)
		}
	}
	q.Set("page", strconv.Itoa(page))
	return r.URL.Path + "?" + q.Encode()
}
func listSearch(key, term string) (string, []any) {
	term = strings.TrimSpace(term)
	if term == "" {
		return "", nil
	}
	// Escape LIKE's special characters using an explicit portable escape character.
	term = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)
	fields := map[string][]string{"tenants": {"name", "cpf_cnpj", "city"}, "landlords": {"name", "cpf_cnpj", "city"}, "users": {"username"}, "properties": {"t.name", "p.number", "p.address", "p.observation"}}[key]
	parts := []string{}
	args := []any{}
	for _, field := range fields {
		parts = append(parts, field+" LIKE ? ESCAPE '!'")
		args = append(args, "%"+term+"%")
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE (" + strings.Join(parts, " OR ") + ")", args
}

func listTieBreaker(key string) string {
	if key == "properties" {
		return "p.id"
	}
	return "id"
}
func pageRelated(ctx context.Context, db queryer, prefix string, rows []Row) ([]Row, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	marks := make([]string, len(rows))
	args := make([]any, len(rows))
	for i, r := range rows {
		marks[i] = "?"
		args[i] = r["id"]
	}
	return query(ctx, db, prefix+" IN ("+strings.Join(marks, ",")+") ORDER BY id", args...)
}
