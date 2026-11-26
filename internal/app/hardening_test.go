package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginLimitsAndRecovery(t *testing.T) {
	a, _ := testApp(t)
	now := time.Now()
	r := httptest.NewRequest("POST", "/login", nil)
	r.Form = url.Values{"username": {"Admin"}}
	r.RemoteAddr = "192.0.2.1:1234"
	for i := 0; i < 10; i++ {
		if !a.allowLogin(r, now) {
			t.Fatal("budget exhausted early")
		}
	}
	r.RemoteAddr = "192.0.2.2:5678"
	r.Form.Set("username", "admin")
	if a.allowLogin(r, now) {
		t.Fatal("account budget bypassed using another origin/case")
	}
	if !a.allowLogin(r, now.Add(time.Minute)) {
		t.Fatal("window did not recover")
	}
	// An origin cannot bypass its budget by varying account names or forwarded headers.
	for i := 0; i < 60; i++ {
		r.Form.Set("username", str(i))
		a.allowLogin(r, now.Add(time.Minute))
	}
	r.Form.Set("username", "another")
	r.Header.Set("X-Forwarded-For", "192.0.2.99")
	if a.allowLogin(r, now.Add(time.Minute)) {
		t.Fatal("origin budget bypassed")
	}
}
func TestLoginRateLimitHTTPDoesNotQueryDatabase(t *testing.T) {
	a, m := testApp(t)
	w := httptest.NewRecorder()
	sess := a.newSession(w, "")
	cookie := w.Result().Cookies()[0]
	r := httptest.NewRequest("POST", "/login", nil)
	r.Form = url.Values{"username": {"admin"}}
	now := time.Now()
	for i := 0; i < 10; i++ {
		a.allowLogin(r, now)
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{"username": {"admin"}, "_csrf": {sess.CSRF}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, req)
	if w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("got %d", w.Code)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestExpiredSessionRejectedBetweenSweeps(t *testing.T) {
	a, _ := testApp(t)
	a.nextSessionSweep = time.Now().Add(time.Hour)
	a.sessions["expired"] = session{UserID: "1", Expires: time.Now().Add(-time.Second)}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "expired"})
	if id, _ := a.session(r); id != "" {
		t.Fatal("expired session renewed")
	}
}
func TestPasswordChangeRequiresCurrentAndRevokesOtherSessions(t *testing.T) {
	a, m := testApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("old password"), 10)
	user := Row{"id": 1, "password": string(hash)}
	for id, uid := range map[string]string{"one": "1", "two": "1", "other": "2"} {
		a.sessions[id] = session{UserID: uid, Expires: time.Now().Add(time.Hour)}
	}
	request := func(current string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/password/update", nil)
		r.Form = url.Values{"password": {"new password"}, "confirmPassword": {"new password"}, "currentPassword": {current}}
		w := httptest.NewRecorder()
		a.password(w, r, view{User: user})
		return w
	}
	if w := request("wrong"); !strings.Contains(w.Body.String(), "Senha atual incorreta") {
		t.Fatal("current password not checked")
	}
	m.ExpectExec("UPDATE tb_users SET password").WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 1, string(hash)).WillReturnResult(sqlmock.NewResult(0, 1))
	w := request("old password")
	if w.Code != 303 {
		t.Fatal(w.Body.String())
	}
	if _, ok := a.sessions["one"]; ok {
		t.Fatal("old current session survived")
	}
	if _, ok := a.sessions["two"]; ok {
		t.Fatal("other session survived")
	}
	if _, ok := a.sessions["other"]; !ok {
		t.Fatal("another user was logged out")
	}
	if len(w.Result().Cookies()) != 1 {
		t.Fatal("missing replacement session")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestOldCredentialCannotAuthenticateAfterReset(t *testing.T) {
	a, m := testApp(t)
	w := httptest.NewRecorder()
	a.newSessionFor(w, "1", "old-hash")
	m.ExpectQuery("SELECT \\* FROM `tb_users`").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "password", "role"}).AddRow(1, "new-hash", "ADMIN"))
	r := httptest.NewRequest("GET", "/users", nil)
	r.AddCookie(w.Result().Cookies()[0])
	w = httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "/login" {
		t.Fatal("session created during reset remained valid")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestLastAdminDemotionAndDeletionRollback(t *testing.T) {
	for _, operation := range []string{"demote", "delete"} {
		t.Run(operation, func(t *testing.T) {
			a, m := testApp(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT id,role FROM tb_users ORDER BY id FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow(1, "ADMIN").AddRow(2, "OPERADOR"))
			m.ExpectRollback()
			var err error
			if operation == "demote" {
				err = a.save(context.Background(), specs["users"], url.Values{"id": {"1"}, "username": {"admin"}, "role": {"LEITOR"}})
			} else {
				err = a.delete(context.Background(), specs["users"], "1", Row{"id": 2})
			}
			if !errors.Is(err, ErrLastAdmin) {
				t.Fatalf("got %v", err)
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestConflictingEditsNeverWrite(t *testing.T) {
	for _, key := range []string{"tenants", "properties", "energies"} {
		t.Run(key, func(t *testing.T) {
			a, m := testApp(t)
			m.ExpectBegin()
			table := specs[key].Table
			if key == "energies" {
				m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `" + table + "` ORDER BY id DESC LIMIT 2 FOR UPDATE")).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			} else {
				m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `" + table + "` WHERE id = ? FOR UPDATE")).WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "changed"))
				if key == "tenants" {
					m.ExpectQuery("SELECT \\* FROM tb_contacts WHERE tenant_id=\\? ORDER BY id FOR UPDATE").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "contact"}).AddRow(10, "changed contact"))
				}
			}
			m.ExpectRollback()
			f := url.Values{"id": {"1"}, "_version": {recordVersion(Row{"id": int64(1), "name": "original"}, nil)}, "name": {"user edit"}, "propertyType": {"LOJA"}, "counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}}
			if err := a.save(context.Background(), specs[key], f); !errors.Is(err, ErrConflict) {
				t.Fatalf("got %v", err)
			}
			if err := m.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestInvalidDomainData(t *testing.T) {
	cases := []struct {
		key string
		f   url.Values
	}{
		{"properties", url.Values{"propertyType": {"LOJA"}, "value": {"-1"}}},
		{"properties", url.Values{"propertyType": {"LOJA"}, "paymentDay": {"32"}}},
		{"properties", url.Values{"propertyType": {"LOJA"}, "paymentDay": {"abc"}}},
		{"tenants", url.Values{"name": {"tenant"}, "cpfCnpj": {"123"}}},
		{"tenants", url.Values{"name": {"tenant"}, "email": {"invalid"}}},
		{"tenants", url.Values{"name": {strings.Repeat("á", 256)}}},
	}
	for _, c := range cases {
		if _, e := validateFields(specs[c.key], c.f, false); e == nil {
			t.Fatalf("accepted %v", c)
		}
	}
	if _, e := validateFields(specs["tenants"], url.Values{"name": {"tenant"}, "cpfCnpj": {"123.456.789-01"}}, false); e != nil {
		t.Fatal("formatted legacy documents must remain accepted", e)
	}
}
func TestFormattedDocumentDuplicateRollsBack(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT id FROM `tb_tenants` WHERE REPLACE").WithArgs("12345678901", "0").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(3))
	m.ExpectRollback()
	err := a.save(context.Background(), specs["tenants"], url.Values{"name": {"duplicate"}, "cpfCnpj": {"123.456.789-01"}})
	if err == nil || !strings.Contains(err.Error(), "Já existe") {
		t.Fatalf("got %v", err)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func ipcaDates() (time.Time, time.Time) {
	return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
}
func TestIPCACacheCoalescesConcurrentRequests(t *testing.T) {
	a, _ := testApp(t)
	var calls atomic.Int32
	a.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"valor":"1,5"},{"valor":"-0.2"}]`)), Header: make(http.Header)}, nil
	})}
	start, end := ipcaDates()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := a.ipca(context.Background(), start, end)
			if e != nil || v < 1.29 || v > 1.30 {
				t.Errorf("%v %v", v, e)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("made %d requests", calls.Load())
	}
}
func TestIPCAFailureIsNeverZeroCorrection(t *testing.T) {
	for _, body := range []string{`[]`, `null`, `invalid`, `[{"valor":"NaN"}]`, `[{"valor":"+Inf"}]`, `[{"valor":"-100"}]`, strings.Repeat(" ", (2<<20)+1)} {
		t.Run(str(len(body)), func(t *testing.T) {
			a, _ := testApp(t)
			a.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			start, end := ipcaDates()
			if _, e := a.ipca(context.Background(), start, end); e == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
	a, _ := testApp(t)
	a.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	start, end := ipcaDates()
	if _, e := a.ipca(context.Background(), start, end); e == nil {
		t.Fatal("404 became zero correction")
	}
}

func TestPaginationAndSearchAreBoundedAndParameterized(t *testing.T) {
	a, m := testApp(t)
	rows := sqlmock.NewRows([]string{"id", "username", "role"})
	for i := 1; i <= 51; i++ {
		rows.AddRow(i, "User "+str(i), "LEITOR")
	}
	term := "_%' OR 1=1 --"
	pattern := regexp.QuoteMeta("SELECT * FROM `tb_users` WHERE (username LIKE ? ESCAPE '!') ORDER BY username, id LIMIT 51 OFFSET 50")
	m.ExpectQuery(pattern).WithArgs("%!_!%' OR 1=1 --%").WillReturnRows(rows)
	w := httptest.NewRecorder()
	a.list(w, httptest.NewRequest("GET", "/users?page=2&q="+url.QueryEscape(term), nil), view{Key: "users", User: Row{"role": "ADMIN"}}, specs["users"])
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Página 2") || strings.Contains(body, "User 51") || !strings.Contains(body, "Próxima") || !strings.Contains(body, "Anterior") {
		t.Fatalf("pagination failed: %d", w.Code)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{"0", "-1", "abc", "100001"} {
		w = httptest.NewRecorder()
		a.list(w, httptest.NewRequest("GET", "/users?page="+raw, nil), view{}, specs["users"])
		if w.Code != 400 {
			t.Fatalf("accepted page %s", raw)
		}
	}
}
func TestEditVersionDoesNotIncludePlaceholderContact(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery("SELECT \\* FROM `tb_tenants`").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Maria"))
	m.ExpectQuery("SELECT \\* FROM tb_contacts WHERE tenant_id=\\? ORDER BY id").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	w := httptest.NewRecorder()
	a.form(w, httptest.NewRequest("GET", "/tenants/edit/1", nil), view{Key: "tenants"}, specs["tenants"], "1", false)
	want := recordVersion(Row{"id": int64(1), "name": "Maria"}, nil)
	if !strings.Contains(w.Body.String(), `name="_version" value="`+want+`"`) {
		t.Fatal("empty contact placeholder polluted version", w.Body.String())
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestHTTPRejectsEditsWithoutSnapshot(t *testing.T) {
	a, m := testApp(t)
	a.sessions["admin"] = session{UserID: "1", CSRF: "csrf", Expires: time.Now().Add(time.Hour)}
	m.ExpectQuery("SELECT \\* FROM `tb_users`").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow(1, "ADMIN"))
	r := httptest.NewRequest("POST", "/properties/save", strings.NewReader("id=1&_csrf=csrf&propertyType=LOJA"))
	r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "admin"})
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 409 {
		t.Fatalf("got %d", w.Code)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestContractAndPropertyInfoHandlers(t *testing.T) {
	a, m := testApp(t)
	property := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "tenant_id", "payment_day", "rental_value", "property_type"}).AddRow(1, 2, "10", 1000, "LOJA")
	}
	tenant := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "name"}).AddRow(2, "Tenant <script>") }
	landlord := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "name"}).AddRow(3, "Landlord") }
	m.ExpectQuery("SELECT \\* FROM `tb_properties`").WithArgs("1").WillReturnRows(property())
	m.ExpectQuery("SELECT \\* FROM `tb_tenants`").WithArgs(int64(2)).WillReturnRows(tenant())
	m.ExpectQuery("SELECT \\* FROM tb_landlords").WillReturnRows(landlord())
	w := httptest.NewRecorder()
	a.contract(w, httptest.NewRequest("GET", "/contracts/form/1", nil), view{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Gerar contrato") {
		t.Fatal(w.Body.String())
	}
	m.ExpectQuery("SELECT \\* FROM `tb_properties`").WithArgs("1").WillReturnRows(property())
	m.ExpectQuery("SELECT \\* FROM `tb_tenants`").WithArgs(int64(2)).WillReturnRows(tenant())
	m.ExpectQuery("SELECT \\* FROM `tb_landlords`").WithArgs("3").WillReturnRows(landlord())
	r := httptest.NewRequest("POST", "/contracts/editor", nil)
	r.Form = url.Values{"propertyId": {"1"}, "landlordId": {"3"}, "startYear": {"2026"}, "endYear": {"2027"}, "startMonth": {"1"}, "endMonth": {"1"}}
	w = httptest.NewRecorder()
	a.contract(w, r, view{})
	if w.Code != 200 || strings.Contains(w.Body.String(), "Tenant <script>") || !strings.Contains(w.Body.String(), "Tenant &lt;script&gt;") {
		t.Fatal(w.Body.String())
	}
	m.ExpectQuery("SELECT \\* FROM `tb_properties`").WithArgs("1").WillReturnRows(property())
	m.ExpectQuery("SELECT \\* FROM `tb_tenants`").WithArgs(int64(2)).WillReturnRows(tenant())
	m.ExpectQuery("SELECT \\* FROM tb_contacts").WithArgs(int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectQuery("SELECT \\* FROM tb_properties_logs").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "rental_value"}).AddRow(1, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), 1000))
	a.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	w = httptest.NewRecorder()
	a.info(w, httptest.NewRequest("GET", "/infopage/1", nil), view{}, "1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Correção indisponível") {
		t.Fatal(w.Body.String())
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestEnergyHistoryPaginationCannotOfferOldEdit(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tb_energy` ORDER BY id DESC, id LIMIT 51 OFFSET 50")).WillReturnRows(sqlmock.NewRows([]string{"id", "bill_amount"}).AddRow(20, 100))
	commercial := sqlmock.NewRows([]string{"id"})
	for i := 100; i > 49; i-- {
		commercial.AddRow(i)
	}
	m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM tb_commercial_energy ORDER BY id DESC LIMIT 51 OFFSET 50")).WillReturnRows(commercial)
	m.ExpectQuery("SELECT \\* FROM tb_energy_title").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	w := httptest.NewRecorder()
	a.list(w, httptest.NewRequest("GET", "/energies?history=1&page=2", nil), view{Key: "energies", User: Row{"role": "ADMIN"}}, specs["energies"])
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, `href="/energies/edit/`) || strings.Contains(body, `href="/energies/edit-commercial/`) || !strings.Contains(body, "Próxima") || !strings.Contains(body, "history=1") {
		t.Fatal("historical page exposed edit or lost navigation", body)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestDeadlockedEnergyWriteRetriesWholeTransaction(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM `tb_energy` ORDER BY id DESC LIMIT 2 FOR UPDATE").WillReturnError(&mysql.MySQLError{Number: 1213, Message: "Deadlock"})
	m.ExpectRollback()
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM `tb_energy` ORDER BY id DESC LIMIT 2 FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id"}))
	m.ExpectExec("INSERT INTO `tb_energy`").WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectCommit()
	if err := a.save(context.Background(), specs["energies"], url.Values{"counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}}); err != nil {
		t.Fatal(err)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
func TestNewPasswordRulesKeepLegacyLogin(t *testing.T) {
	if _, err := validateFields(specs["users"], url.Values{"username": {"user"}, "role": {"LEITOR"}, "password": {"short"}}, false); err == nil {
		t.Fatal("new short password accepted")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("short"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !validLogin([]Row{{"password": string(hash)}}, "short") {
		t.Fatal("legacy short password rejected")
	}
}

func TestConcurrentPasswordResetDoesNotGetOverwritten(t *testing.T) {
	a, m := testApp(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("old password"), 10)
	m.ExpectExec(regexp.QuoteMeta("UPDATE tb_users SET password=?,updated_at=? WHERE id=? AND password=?")).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), 1, string(hash)).WillReturnResult(sqlmock.NewResult(0, 0))
	r := httptest.NewRequest("POST", "/password/update", nil)
	r.Form = url.Values{"password": {"new password"}, "confirmPassword": {"new password"}, "currentPassword": {"old password"}}
	w := httptest.NewRecorder()
	a.password(w, r, view{User: Row{"id": 1, "password": string(hash)}})
	if w.Code != 409 || len(w.Result().Cookies()) != 0 {
		t.Fatal("concurrent reset overwritten or a new session issued")
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidContactsCannotBeSilentlyDroppedOrTruncated(t *testing.T) {
	for _, form := range []url.Values{
		{"name": {"Maria"}, "contacts[1000].contact": {"ignored"}},
		{"name": {"Maria"}, "contacts[-1].contact": {"ignored"}},
		{"name": {"Maria"}, "contacts[0].contact": {strings.Repeat("a", 256)}, "contacts[0].type": {"CELULAR"}},
		{"name": {"Maria"}, "contacts[0].contact": {"not-an-email"}, "contacts[0].type": {"EMAIL"}},
	} {
		if _, err := validateFields(specs["tenants"], form, false); err == nil {
			t.Fatalf("accepted %v", form)
		}
	}
}

func TestTenantPageLoadsOnlyItsContactsAndProperties(t *testing.T) {
	a, m := testApp(t)
	m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM `tb_tenants` ORDER BY name, id LIMIT 51 OFFSET 0")).WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Ana").AddRow(2, "Maria"))
	m.ExpectQuery(regexp.QuoteMeta("SELECT * FROM tb_contacts WHERE tenant_id IN (?,?) ORDER BY id")).WithArgs(int64(1), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "contact"}).AddRow(10, 1, "ana@example.com").AddRow(20, 2, "maria@example.com"))
	m.ExpectQuery(regexp.QuoteMeta("SELECT id,tenant_id FROM tb_properties WHERE tenant_id IN (?,?) ORDER BY id")).WithArgs(int64(1), int64(2)).WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id"}).AddRow(10, 1))
	w := httptest.NewRecorder()
	a.list(w, httptest.NewRequest("GET", "/tenants", nil), view{Key: "tenants", User: Row{"role": "ADMIN"}}, specs["tenants"])
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	rows := strings.Split(w.Body.String(), "<tr>")
	found := false
	for _, row := range rows {
		if strings.Contains(row, `title="Ana"`) {
			found = true
			if !strings.Contains(row, "ana@example.com") || strings.Contains(row, "maria@example.com") {
				t.Fatal("contact assigned to wrong tenant")
			}
		}
	}
	if !found {
		t.Fatal("tenant row missing")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
