package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordsPreserveLeadingAndTrailingSpaces(t *testing.T) {
	for _, editing := range []bool{false, true} {
		password := " senha com espaços "
		values, e := validateFields(specs["users"], url.Values{"username": {"admin"}, "role": {"ADMIN"}, "password": {password}}, editing)
		if e != nil {
			t.Fatal(e)
		}
		if e = bcrypt.CompareHashAndPassword([]byte(str(values["password"])), []byte(password)); e != nil {
			t.Fatalf("password was modified: %v", e)
		}
		if bcrypt.CompareHashAndPassword([]byte(str(values["password"])), []byte(strings.TrimSpace(password))) == nil {
			t.Fatal("trimmed password must not authenticate")
		}
	}
}

func TestPasswordChangeRejectsWhitespaceWithoutWriting(t *testing.T) {
	a, m := testApp(t)
	r := httptest.NewRequest("POST", "/password/update", nil)
	r.Form = url.Values{"password": {"   "}, "confirmPassword": {"   "}}
	w := httptest.NewRecorder()
	a.password(w, r, view{User: Row{"id": 1}})
	if !strings.Contains(w.Body.String(), "Preencha senhas iguais") {
		t.Fatal("blank password was accepted")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestPrintStylesAreIsolated(t *testing.T) {
	a, _ := testApp(t)
	for _, page := range []string{"home", "energy", "receipts", "contract"} {
		t.Run(page, func(t *testing.T) {
			var out bytes.Buffer
			v := view{Page: page, Record: Row{}, Extra: Row{"landlord": Row{}, "tenant": Row{}}, User: Row{"role": "ADMIN"}}
			if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
				t.Fatal(e)
			}
			hasContract := strings.Contains(out.String(), `href="/css/contract-editor.css"`)
			if hasContract != (page == "contract") {
				t.Fatalf("contract print stylesheet leaked into %s", page)
			}
		})
	}
}

func TestRejectedFormKeepsMainCheckbox(t *testing.T) {
	a, _ := testApp(t)
	var out bytes.Buffer
	v := view{Page: "form", Key: "landlords", Fields: specs["landlords"].Fields, Options: map[string][][2]string{}, Record: Row{"main": "true"}}
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), `name="main" value="true" checked`) {
		t.Fatal("submitted checkbox lost after validation error")
	}
}

func TestSessionUsesIdleTimeoutAndSessionCookie(t *testing.T) {
	a, _ := testApp(t)
	a.cfg.SessionHours = 0
	a.cfg.SessionTimeoutSeconds = 90
	a.sessions["active"] = session{UserID: "1", CSRF: "token", Expires: time.Now().Add(10 * time.Second)}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "active"})
	id, s := a.session(r)
	if id != "active" || time.Until(s.Expires) < 89*time.Second {
		t.Fatal("active session did not extend its idle timeout")
	}
	w := httptest.NewRecorder()
	a.newSession(w, "1")
	cookie := w.Result().Cookies()[0]
	if !cookie.Expires.IsZero() || cookie.MaxAge != 0 {
		t.Fatal("session cookie must not outlive browser session")
	}
	if (Config{}).sessionTimeout() != 30*time.Minute {
		t.Fatal("Default idle timeout is 30 minutes")
	}
	if (Config{SessionHours: 12}).sessionTimeout() != 12*time.Hour {
		t.Fatal("legacy JSON compatibility lost")
	}
}

func TestNativeEnumSortUsesDisplayNames(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery("SELECT p.\\*,t.name AS tenant_name FROM tb_properties p LEFT JOIN tb_tenants t ON t.id=p.tenant_id ORDER BY CASE p.property_type").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectQuery("SELECT \\* FROM tb_landlords ORDER BY name").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	w := httptest.NewRecorder()
	a.list(w, httptest.NewRequest("GET", "/properties?sortField=propertyType", nil), view{Key: "properties"}, specs["properties"])
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestMalformedRoutesCannotSaveOrDelete(t *testing.T) {
	a, m := testApp(t)
	a.sessions["admin"] = session{UserID: "1", CSRF: "token", Expires: time.Now().Add(time.Hour)}
	for _, path := range []string{"/password/anything", "/tenants/save/extra", "/properties/delete/3/extra"} {
		m.ExpectQuery("SELECT \\* FROM `tb_users` WHERE id = \\?").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow(1, "ADMIN"))
		r := httptest.NewRequest("POST", path, strings.NewReader("_csrf=token&password=p&confirmPassword=p"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "admin"})
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Fatalf("%s: got %d", path, w.Code)
		}
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestInstantAndCivilDateCompatibility(t *testing.T) {
	a, m := testApp(t)
	cols := []*sqlmock.Column{sqlmock.NewColumn("date_of_birth").OfType("DATE", time.Time{}), sqlmock.NewColumn("created_at").OfType("DATETIME", time.Time{})}
	instant := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	m.ExpectQuery("SELECT dates").WillReturnRows(sqlmock.NewRowsWithColumnDefinition(cols...).AddRow(time.Date(1980, 2, 29, 0, 0, 0, 0, time.UTC), instant))
	rows, e := query(context.Background(), a.db, "SELECT dates")
	if e != nil {
		t.Fatal(e)
	}
	if rows[0]["date_of_birth"] != "1980-02-29" {
		t.Fatal("civil date shifted across timezone")
	}
	if !rows[0]["created_at"].(time.Time).Equal(instant) {
		t.Fatal("Instant changed")
	}
	tpl, e := a.tpl.Clone()
	if e != nil {
		t.Fatal(e)
	}
	tpl, e = tpl.Parse(`{{define "dates"}}{{date .date_of_birth}}|{{s .created_at}}|{{date .created_at}}{{end}}`)
	if e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e = tpl.ExecuteTemplate(&out, "dates", rows[0]); e != nil {
		t.Fatal(e)
	}
	if out.String() != "1980-02-29|2026-10-24 22:00:00|2026-10-24" {
		t.Fatalf("date presentation: %s", out.String())
	}
	dsn, e := (DatabaseConfig{Host: "localhost", Port: "3306", Name: "db_aluguel", Timezone: "America/Sao_Paulo"}).DSN()
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if cfg.Loc != time.UTC {
		t.Fatal("Audit timestamps must use UTC")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestIPCAReferenceUsesTimestampRatherThanID(t *testing.T) {
	old := Row{"id": 100, "created_at": time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "rental_value": 1000}
	recent := Row{"id": 1, "created_at": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "rental_value": 1500}
	irrelevant := Row{"id": 101, "created_at": time.Now(), "rental_value": 1500, "address": "Endereço alterado"}
	got := mostRecentRelevantLog([]Row{irrelevant, old, recent})
	if got["id"] != 1 {
		t.Fatal("IPCA picked highest ID instead of most recent change")
	}
	a, _ := testApp(t)
	today := a.now()
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	// A canceled context would fail an HTTP request. Today's reference needs none.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if n, e := a.ipca(ctx, start, today); e != nil || n != 0 {
		t.Fatalf("same-day IPCA: %v %v", n, e)
	}
}

func TestMissingMoneyIsNotDisplayedAsZero(t *testing.T) {
	if money(nil) != "—" {
		t.Fatal("NULL must not imply a calculated zero")
	}
	if money(0) != "R$ 0,00" || money(1234.56) != "R$ 1.234,56" {
		t.Fatal("Brazilian currency formatting changed")
	}
}

func TestWordsUsesDirectDecimalRounding(t *testing.T) {
	// Round the original floating-point value directly to two decimal places.
	cases := []struct {
		value float64
		want  string
	}{{2.675, "dois reais e sessenta e sete centavos"}, {1.125, "um real e doze centavos"}, {1.005, "um real"}, {0.125, "doze centavos"}}
	for _, c := range cases {
		if got := words(c.value); got != c.want {
			t.Fatalf("%v: got %q want %q", c.value, got, c.want)
		}
	}
}
