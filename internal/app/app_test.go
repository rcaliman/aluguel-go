package app

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"golang.org/x/crypto/bcrypt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) (*App, sqlmock.Sqlmock) {
	t.Helper()
	db, m, e := sqlmock.New()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	a, e := New(db, Config{SessionHours: 12, Database: DatabaseConfig{Timezone: "America/Sao_Paulo"}})
	if e != nil {
		t.Fatal(e)
	}
	return a, m
}
func TestAllPagesRenderAndEscape(t *testing.T) {
	a, _ := testApp(t)
	u := Row{"id": 1, "username": "admin", "role": "ADMIN"}
	rec := Row{"id": 1, "name": "<script>alert(1)</script>", "date": "2026-10-25", "property_type": "LOJA", "payment_day": "10", "rental_value": 1000, "tenant_id": 1, "tenant_name": "Maria", "amount_1": 30}
	for _, page := range []string{"home", "login", "form", "list", "energy", "password", "contractform", "contract", "info", "receipts"} {
		t.Run(page, func(t *testing.T) {
			opts := map[string][][2]string{}
			for _, f := range specs["tenants"].Fields {
				opts[f.Kind] = enumOptions(f.Kind)
			}
			v := view{Page: page, Title: "Teste", Key: "tenants", User: u, CSRF: "token", Spec: specs["tenants"], Fields: specs["tenants"].Fields, Record: rec, Rows: []Row{rec}, Contacts: []Row{{"contact": "555", "contact_type": "CELULAR"}}, Landlords: []Row{rec}, Tenants: []Row{rec}, Commercial: []Row{rec}, Logs: []Row{rec}, Options: opts, Extra: Row{"landlord": rec, "tenant": rec, "property": rec, "words": "mil reais"}, Year: 2026, Month: 10}
			var b bytes.Buffer
			if e := a.tpl.ExecuteTemplate(&b, "layout", v); e != nil {
				t.Fatal(e)
			}
			if strings.Contains(b.String(), "<script>alert(1)</script>") {
				t.Fatal("unescaped user content")
			}
			if strings.Contains(b.String(), "ZgotmplZ") {
				t.Fatal("invalid URL")
			}
		})
	}
	for _, key := range []string{"properties", "users", "landlords", "commercial", "titles"} {
		t.Run(key, func(t *testing.T) {
			opts := map[string][][2]string{}
			for _, f := range specs[key].Fields {
				opts[f.Kind] = enumOptions(f.Kind)
			}
			v := view{Page: "form", Key: key, Fields: specs[key].Fields, Record: rec, User: u, Options: opts}
			var b bytes.Buffer
			if e := a.tpl.ExecuteTemplate(&b, "layout", v); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestEnergyCalculationRules(t *testing.T) {
	tests := []struct {
		name, key string
		v, p      Row
		want      []float64
		bad       bool
	}{
		{"residential proportional", "energies", Row{"counter_1": 200, "counter_2": 300, "counter_3": 400, "bill_amount": 600}, Row{"counter_1": 100, "counter_2": 100, "counter_3": 100}, []float64{100, 200, 300}, false},
		{"counter replaced", "energies", Row{"counter_1": 20, "counter_2": 200, "counter_3": 300, "bill_amount": 220}, Row{"counter_1": 100, "counter_2": 100, "counter_3": 200}, []float64{20, 100, 100}, false},
		{"commercial proportional", "commercial", Row{"internal_counter": 150, "account_consumption": 200, "account_value": 400}, Row{"internal_counter": 100}, []float64{100, 300}, false},
		{"commercial zero rejected", "commercial", Row{"internal_counter": 150, "account_consumption": 0}, Row{"internal_counter": 100}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := calculateEnergy(tc.key, tc.v, tc.p)
			if (e != nil) != tc.bad {
				t.Fatalf("error %v", e)
			}
			for i, want := range tc.want {
				k := []string{"amount_1", "amount_2", "amount_3"}[i]
				if math.Abs(num(tc.v[k])-want) > 0.000001 {
					t.Fatalf("%s: got %v want %v", k, tc.v[k], want)
				}
			}
		})
	}
	v := Row{"counter_1": 100}
	if e := calculateEnergy("energies", v, nil); e != nil {
		t.Fatal(e)
	}
	if v["amount_1"] != nil {
		t.Fatal("first reading must not have calculated amounts")
	}
}
func TestBCryptPrefixes(t *testing.T) {
	// Fixed cost-10 BCrypt fixture, checked with all supported hash prefixes.
	hash := "$2a$10$pHB8/PINJjYuQxJ.fjgIM.bChKTOPRlR7RSf4TXogjzzANNMyLGMa"
	for _, prefix := range []string{"$2a$", "$2b$", "$2y$"} {
		encoded := prefix + hash[4:]
		if e := bcrypt.CompareHashAndPassword([]byte(encoded), []byte("senha-teste")); e != nil {
			t.Fatal(e)
		}
		if bcrypt.CompareHashAndPassword([]byte(encoded), []byte("incorreta")) == nil {
			t.Fatal("wrong password accepted")
		}
	}
}
func TestLoginCSRFAndRoleChecks(t *testing.T) {
	a, m := testApp(t)
	h := a.Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/login", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	c := cookies[0]
	_, sess := a.session(&http.Request{Header: http.Header{"Cookie": {c.String()}}})
	w = httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/login", strings.NewReader("username=x&password=y"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("CSRF accepted")
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("senha"), 10)
	m.ExpectQuery("SELECT \\* FROM tb_users WHERE username=\\?").WithArgs("maria").WillReturnRows(sqlmock.NewRows([]string{"id", "username", "password", "role"}).AddRow(1, "maria", string(hash), "LEITOR"))
	form := url.Values{"username": {"maria"}, "password": {"senha"}, "_csrf": {sess.CSRF}}
	req = httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 303 {
		t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	auth := w.Result().Cookies()[0]
	if auth.Value == c.Value {
		t.Fatal("session not rotated")
	}
	for _, path := range []string{"/users", "/tenants/new", "/properties/delete/3"} {
		m.ExpectQuery("SELECT \\* FROM `tb_users` WHERE id = \\?").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "username", "role"}).AddRow(1, "maria", "LEITOR"))
		req = httptest.NewRequest("GET", path, nil)
		req.AddCookie(auth)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s: got %d", path, w.Code)
		}
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestTenantDeletionDetachesPropertyInTransaction(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM `tb_tenants` WHERE id = \\?").WithArgs("8").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(8, "Maria"))
	m.ExpectQuery("SELECT \\* FROM tb_properties WHERE tenant_id=\\? ORDER BY id FOR UPDATE").WithArgs("8").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("UPDATE tb_properties SET tenant_id=NULL WHERE tenant_id=\\?").WithArgs("8").WillReturnResult(sqlmock.NewResult(0, 2))
	m.ExpectExec("DELETE FROM tb_contacts WHERE tenant_id=\\?").WithArgs("8").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("DELETE FROM `tb_tenants` WHERE id=\\?").WithArgs("8").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := a.delete(context.Background(), specs["tenants"], "8", Row{"id": 1}); e != nil {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestFailedDeletionRollsBack(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM `tb_properties` WHERE id = \\?").WithArgs("9").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	m.ExpectExec("DELETE FROM tb_properties_logs WHERE property_id=\\?").WithArgs("9").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("DELETE FROM `tb_properties` WHERE id=\\?").WithArgs("9").WillReturnError(sql.ErrConnDone)
	m.ExpectRollback()
	if e := a.delete(context.Background(), specs["properties"], "9", Row{"id": 1}); e == nil {
		t.Fatal("expected failure")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestDocumentsAndDates(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want string
	}{{0, "zero reais"}, {1, "um real"}, {1234.56, "mil duzentos e trinta e quatro reais e cinquenta e seis centavos"}, {100, "cem reais"}, {0.01, "um centavo"}} {
		if got := words(tc.v); got != tc.want {
			t.Fatalf("%v got %q want %q", tc.v, got, tc.want)
		}
	}
	if document("12345678901") != "123.456.789-01" {
		t.Fatal("CPF")
	}
	if _, e := validDate(2026, 2, "31"); e == nil {
		t.Fatal("invalid day accepted")
	}
	if _, e := validDate(2028, 2, "29"); e != nil {
		t.Fatal(e)
	}
}
func TestJSONConfigAndDriverTimezone(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if e := os.WriteFile(p, []byte(`{"database":{"name":"db_aluguel","username":"teste","password":"p@ss:/?&"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := LoadConfig(p)
	if e != nil {
		t.Fatal(e)
	}
	dsn, e := c.Database.DSN()
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(dsn, "loc=America%2FSao_Paulo") || !strings.Contains(dsn, "parseTime=true") {
		t.Fatal("Audit timestamps require UTC persistence and parsed values")
	}
	if strings.Contains(dsn, "time_zone=") {
		t.Fatal("must not change MySQL session timezone")
	}
	if c.Address != ":8080" {
		t.Fatal(c.Address)
	}
}
func TestStaticAssetsAndAnonymousRedirect(t *testing.T) {
	a, _ := testApp(t)
	for _, path := range []string{"/vendor/bootstrap.min.css", "/vendor/bootstrap.bundle.min.js", "/js/go-app.js", "/css/style.css", "/manifest.json"} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/properties", nil))
	if w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal("protected page accessible anonymously")
	}
}
func TestExpiredSession(t *testing.T) {
	a, _ := testApp(t)
	a.sessions["expired"] = session{UserID: "1", Expires: time.Now().Add(-time.Second)}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "expired"})
	id, _ := a.session(r)
	if id != "" {
		t.Fatal("expired session accepted")
	}
}

func TestMySQLBitBoolean(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery("SELECT main FROM tb_landlords").WillReturnRows(sqlmock.NewRows([]string{"main"}).AddRow([]byte{1}).AddRow([]byte{0}))
	rows, e := query(context.Background(), a.db, "SELECT main FROM tb_landlords")
	if e != nil {
		t.Fatal(e)
	}
	if rows[0]["main"] != true || rows[1]["main"] != false {
		t.Fatalf("unexpected BIT conversion: %v", rows)
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestPropertyWriteAndHistoryAreAtomic(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO `tb_properties`").WillReturnResult(sqlmock.NewResult(42, 1))
	m.ExpectQuery("SELECT \\* FROM `tb_tenants` WHERE id = \\?").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "cpf_cnpj"}).AddRow(7, "Maria", "12345678901"))
	m.ExpectExec("INSERT INTO `tb_properties_logs` \\(`address`,`complement`,`created_at`,`number`,`observation`,`payment_day`,`property_id`,`property_type`,`rental_value`,`tenant_cpf_cnpj`,`tenant_name`\\)").WithArgs(nil, nil, sqlmock.AnyArg(), "101", nil, "10", "42", "APARTAMENTO", 1000.0, "12345678901", "Maria").WillReturnError(sql.ErrConnDone)
	m.ExpectRollback()
	e := a.save(context.Background(), specs["properties"], url.Values{"propertyType": {"APARTAMENTO"}, "tenant": {"7"}, "number": {"101"}, "paymentDay": {"10"}, "value": {"1000"}})
	if e == nil {
		t.Fatal("history failure must roll back property insert")
	}
	if e = m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestReceiptCondominiumDecember(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery("SELECT \\* FROM `tb_landlords`").WithArgs("2").WillReturnRows(sqlmock.NewRows([]string{"id", "name", "cpf_cnpj", "city", "state"}).AddRow(2, "Locador", "12345678901", "Colatina", "ES"))
	m.ExpectQuery("SELECT \\* FROM `tb_properties`").WithArgs("3").WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "property_type", "rental_value", "payment_day", "observation"}).AddRow(3, 1, "CONDOMINIO", 100, "10", "unidade 101"))
	m.ExpectQuery("SELECT \\* FROM `tb_tenants`").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "name", "cpf_cnpj"}).AddRow(1, "Maria <script>", "12345678901"))
	m.ExpectQuery("SELECT contact FROM tb_contacts").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"contact"}).AddRow("555-0100"))
	r := httptest.NewRequest("POST", "/receipts/generate", nil)
	r.Form = url.Values{"propertyIds": {"3", "3"}, "landlordId": {"2"}, "month": {"Dezembro"}, "year": {"2026"}}
	w := httptest.NewRecorder()
	a.receipts(w, r, view{CSRF: "token"})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	body := w.Body.String()
	if strings.Count(body, "10 de Janeiro de 2027") != 2 {
		t.Fatal("expected two copies dated next January")
	}
	if strings.Count(body, "class=\"receipt-copy") != 2 {
		t.Fatal("expected two copies, deduplicating selected IDs")
	}
	if strings.Contains(body, "Maria <script>") || !strings.Contains(body, "Maria &lt;script&gt;") {
		t.Fatal("receipt not escaped")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestBlankOptionalFieldsAndPasswordPreservation(t *testing.T) {
	v, e := validateFields(specs["tenants"], url.Values{"name": {"Maria"}, "cpfCnpj": {"  "}}, false)
	if e != nil {
		t.Fatal(e)
	}
	if v["cpf_cnpj"] != nil {
		t.Fatal("blank CPF must be NULL for MySQL unique constraint")
	}
	v, e = validateFields(specs["users"], url.Values{"username": {"maria"}, "role": {"ADMIN"}, "password": {""}}, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := v["password"]; ok {
		t.Fatal("editing without password must preserve hash")
	}
	if _, e = validateFields(specs["users"], url.Values{"username": {"maria"}, "role": {"SUPER_ADMIN"}, "password": {"senha"}}, false); e == nil {
		t.Fatal("invalid enum accepted")
	}
}

func TestWordsRejectOverflow(t *testing.T) {
	for _, v := range []float64{math.MaxFloat64, math.Inf(1), math.NaN()} {
		if words(v) != "valor fora do intervalo suportado" {
			t.Fatal("unbounded monetary conversion")
		}
	}
}
