package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ipcaEntry struct {
	Value   float64
	Err     error
	Expires time.Time
}

func (a *App) ipca(ctx context.Context, start, end time.Time) (float64, error) {
	loc := aLocation(a.cfg)
	start = start.In(loc)
	end = end.In(loc)
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, loc)
	end = time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, loc)
	if !start.Before(end) {
		return 0, nil
	}
	key := start.Format("2006-01-02") + ":" + end.Format("2006-01-02")
	for {
		a.ipcaMu.Lock()
		if entry, ok := a.ipcaCache[key]; ok && time.Now().Before(entry.Expires) {
			a.ipcaMu.Unlock()
			return entry.Value, entry.Err
		}
		if done, ok := a.ipcaFlights[key]; ok {
			a.ipcaMu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
		done := make(chan struct{})
		a.ipcaFlights[key] = done
		a.ipcaMu.Unlock()
		value, err := a.fetchIPCA(ctx, start, end)
		ttl := 6 * time.Hour
		if err != nil {
			ttl = 30 * time.Second
		}
		a.ipcaMu.Lock()
		if len(a.ipcaCache) >= 256 { // Bounded cache; never an unbounded history of date ranges.
			for k := range a.ipcaCache {
				delete(a.ipcaCache, k)
				break
			}
		}
		// A canceled page request must not poison the cache for other visitors.
		if ctx.Err() == nil {
			a.ipcaCache[key] = ipcaEntry{value, err, time.Now().Add(ttl)}
		}
		delete(a.ipcaFlights, key)
		close(done)
		a.ipcaMu.Unlock()
		return value, err
	}
}
func (a *App) fetchIPCA(ctx context.Context, start, end time.Time) (float64, error) {
	q := url.Values{"formato": {"json"}, "dataInicial": {start.Format("02/01/2006")}, "dataFinal": {end.Format("02/01/2006")}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.bcb.gov.br/dados/serie/bcdata.sgs.433/dados?"+q.Encode(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Bacen HTTP %d: correção indisponível", resp.StatusCode)
	}
	const maxResponse = 2 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return 0, err
	}
	if len(body) > maxResponse {
		return 0, fmt.Errorf("resposta Bacen excede o limite")
	}
	var data []struct {
		Valor string `json:"valor"`
	}
	if err = json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("Bacen sem dados para o período")
	}
	factor := 1.0
	for _, r := range data {
		n, err := strconv.ParseFloat(strings.ReplaceAll(r.Valor, ",", "."), 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n <= -100 {
			return 0, fmt.Errorf("índice Bacen inválido")
		}
		factor *= 1 + n/100
		if math.IsNaN(factor) || math.IsInf(factor, 0) {
			return 0, fmt.Errorf("correção fora do intervalo suportado")
		}
	}
	return (factor - 1) * 100, nil
}
