package app

import (
	"aluguel/web"
	"bytes"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uiFixtureViews() map[string]view {
	user := Row{"id": 1, "username": "admin", "role": "ADMIN"}
	tenant := Row{"id": 1, "name": "Maria da Silva", "cpf_cnpj": "12345678901", "document": "1234567", "city": "Colatina", "state": "ES", "nationality": "brasileira", "marital_status": "CASADO", "date_of_birth": "1980-02-29", "address": "Rua das Flores", "location": "Centro", "contacts": []Row{{"id": 1, "contact_type": "CELULAR", "contact": "(27) 99999-1234"}}, "properties": []Row{{"id": 1}}}
	landlord := Row{"id": 1, "name": "José Caliman", "cpf_cnpj": "98765432100", "city": "Colatina", "state": "ES", "main": true}
	property := Row{"id": 1, "property_type": "APARTAMENTO", "use_type": "RESIDENCIAL", "address": "Rua das Flores", "number": "101", "complement": "Bloco A", "location": "Centro", "city": "Colatina", "state": "ES", "tenant_id": 1, "tenant_name": "Maria da Silva", "rental_value": 1234.56, "payment_day": "10", "observation": "Aluguel mensal"}
	energy := Row{"id": 2, "counter_1": 1250, "counter_2": 2450, "counter_3": 3100, "amount_1": 100, "amount_2": 150, "amount_3": 200, "bill_amount": 450, "date": "2026-10-10"}
	commercial := Row{"id": 2, "internal_counter": 1500, "account_consumption": 250, "account_value": 400, "amount_1": 160, "amount_2": 240, "calculated_consumption_1": 100, "calculated_consumption_2": 150, "date": "2026-10-10"}
	logs := []Row{{"id": 1, "created_at": time.Date(2024, 1, 10, 12, 0, 0, 0, time.UTC), "tenant_name": "Maria da Silva", "rental_value": 1234.56}}
	base := view{Title: "Aluguel de Imóveis", User: user, CSRF: "preview-token", Record: property, Extra: tenant, Rows: []Row{property}, Tenants: []Row{tenant}, Landlords: []Row{landlord}, Contacts: tenant["contacts"].([]Row), Logs: logs, Year: 2026, Month: 10, SortField: "tenant.name"}
	views := map[string]view{}
	for name, page := range map[string]string{"home": "home", "login": "login", "password": "password", "info": "info", "contractform": "contractform"} {
		v := base
		v.Page = page
		if page == "login" {
			v.User = nil
		}
		views[name] = v
	}
	for _, key := range []string{"properties", "tenants", "landlords", "users"} {
		v := base
		v.Key = key
		v.Page = "list"
		switch key {
		case "tenants":
			v.Rows = []Row{tenant}
		case "landlords":
			v.Rows = []Row{landlord}
		case "users":
			v.Rows = []Row{user}
		}
		views[key] = v
	}
	for _, key := range []string{"tenants", "landlords", "properties", "users", "energies", "commercial", "titles"} {
		v := base
		v.Key = key
		v.Page = "form"
		v.Spec = specs[key]
		v.Fields = specs[key].Fields
		switch key {
		case "tenants":
			v.Record = tenant
		case "landlords":
			v.Record = landlord
		case "users":
			v.Record = user
		case "energies":
			v.Record = energy
		case "commercial":
			v.Record = commercial
		case "titles":
			v.Record = Row{}
		}
		views[key+"-form"] = v
	}
	v := base
	v.Page = "energy"
	v.Record = Row{"title_amount_1": "Apartamento 101", "title_amount_2": "Apartamento 102", "title_amount_3": "Apartamento 103", "title_amount_4": "Loja 1", "title_amount_5": "Loja 2"}
	v.Rows = []Row{energy}
	v.Commercial = []Row{commercial}
	views["energy"] = v
	v = base
	v.Page = "receipts"
	v.Rows = []Row{{"tenant": "Maria da Silva", "tenant_document": "123.456.789-01", "value": "mil duzentos e trinta e quatro reais e cinquenta e seis centavos", "property_type": "APARTAMENTO", "number": "101", "month": "Outubro", "year": 2026, "next_year": 2027, "next_month": "Novembro", "locale": "Colatina ES", "day": "10", "landlord": "José Caliman", "landlord_document": "987.654.321-00", "contact": "(27) 99999-1234"}}
	views["receipts"] = v
	return views
}
func TestUIPagesAndBindings(t *testing.T) {
	a, _ := testApp(t)
	for name, v := range uiFixtureViews() {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
				t.Fatal(e)
			}
			body := out.String()
			for _, bad := range []string{"th:field=", "th:text=", "sec:authorize=", "ZgotmplZ", "form-grid", "nav{padding"} {
				if strings.Contains(body, bad) {
					t.Fatalf("unconverted or generic markup %s", bad)
				}
			}
			for _, required := range []string{"navbar navbar-expand-lg navbar-dark bg-dark", "/vendor/bootstrap-icons/bootstrap-icons.css", "site-footer"} {
				if !strings.Contains(body, required) {
					t.Fatalf("missing original component %s", required)
				}
			}
		})
	}
	v := uiFixtureViews()["properties-form"]
	var out bytes.Buffer
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), `value="1" selected`) {
		t.Fatal("current tenant selection lost")
	}
	v = uiFixtureViews()["tenants-form"]
	v.Record["email"] = "maria@example.com"
	out.Reset()
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	for _, binding := range []string{`name="contacts[0].contact"`, `value="(27) 99999-1234"`, `value="CASADO" selected`, `value="1980-02-29"`, `name="email"`, `value="maria@example.com"`} {
		if !strings.Contains(out.String(), binding) {
			t.Fatalf("form binding missing: %s", binding)
		}
	}
	v = uiFixtureViews()["users"]
	out.Reset()
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), `name="_csrf" value=""`) {
		t.Fatal("CSRF token lost inside user row loop")
	}
}
func TestExportUIPreview(t *testing.T) {
	dir := os.Getenv("ALUGUEL_UI_PREVIEW_DIR")
	if dir == "" {
		t.Skip("visual preview export not requested")
	}
	a, _ := testApp(t)
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := fs.WalkDir(web.Files, "static", func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		dest := filepath.Join(dir, strings.TrimPrefix(path, "static/"))
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		data, e := web.Files.ReadFile(path)
		if e != nil {
			return e
		}
		return os.WriteFile(dest, data, 0644)
	}); e != nil {
		t.Fatal(e)
	}
	for name, v := range uiFixtureViews() {
		var out bytes.Buffer
		if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
			t.Fatal(e)
		}
		body := out.String()
		for _, prefix := range []string{"/css/", "/js/", "/vendor/", "/icons/"} {
			body = strings.ReplaceAll(body, `="`+prefix, `="file://`+dir+prefix)
		}
		if e := os.WriteFile(filepath.Join(dir, name+".html"), []byte(body), 0644); e != nil {
			t.Fatal(e)
		}
	}
}

func TestRestoredUIResourcesAndReadOnlyActions(t *testing.T) {
	a, _ := testApp(t)
	for _, path := range []string{"/vendor/bootstrap-icons/bootstrap-icons.css", "/vendor/bootstrap-icons/fonts/bootstrap-icons.woff2", "/vendor/jquery.min.js", "/vendor/jquery.mask.min.js"} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("missing embedded resource %s", path)
		}
	}
	v := uiFixtureViews()["properties"]
	v.User = Row{"id": 2, "username": "leitor", "role": "LEITOR"}
	var out bytes.Buffer
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), `data-delete=`) || strings.Contains(out.String(), `href="/properties/new"`) {
		t.Fatal("read-only user received edit controls")
	}
	v = uiFixtureViews()["users"]
	v.Rows = append(v.Rows, Row{"id": 2, "username": "operador", "role": "OPERADOR"})
	out.Reset()
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	start := strings.Index(out.String(), `action="/users/delete/2"`)
	if start < 0 {
		t.Fatal("missing user deletion form")
	}
	body := out.String()[start:]
	body = body[:strings.Index(body, "</form>")]
	if !strings.Contains(body, `name="_csrf" value="preview-token"`) {
		t.Fatal("row form lost the root CSRF token")
	}
}

func TestRestoredEnergyBindingsAndTitles(t *testing.T) {
	a, _ := testApp(t)
	v := uiFixtureViews()["energy"]
	var out bytes.Buffer
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	for _, value := range []string{"Apartamento 101", "Apartamento 102", "Apartamento 103", "Loja 1", "Loja 2", "1250,00", "2450,00", "3100,00", "100,00", "150,00", "200,00", "22,22%"} {
		if !strings.Contains(out.String(), value) {
			t.Fatalf("energy value disappeared: %s", value)
		}
	}
	if strings.Contains(out.String(), "Rateio indisponível") {
		t.Fatal("valid allocation was mistaken for missing data")
	}
	v = uiFixtureViews()["users-form"]
	v.Record = cloneRow(v.Record)
	v.Record["password"] = "$2a$10$secret-hash"
	out.Reset()
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), "secret-hash") {
		t.Fatal("stored password hash exposed in form")
	}
}

func TestMissingResidentialAllocationDoesNotShowZeroPercent(t *testing.T) {
	a, _ := testApp(t)
	v := uiFixtureViews()["energy"]
	v.Rows = []Row{cloneRow(v.Rows[0])}
	for _, key := range []string{"amount_1", "amount_2", "amount_3"} {
		v.Rows[0][key] = nil
	}
	var out bytes.Buffer
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(out.String(), "Rateio indisponível") || strings.Contains(out.String(), "(0,00%)") {
		t.Fatal("unallocated bill presented as a calculated zero share")
	}
}

func TestEnergyEditIconOnlyOnLastRow(t *testing.T) {
	a, _ := testApp(t)
	v := uiFixtureViews()["energy"]
	first := cloneRow(v.Rows[0])
	first["id"] = 1
	v.Rows = append([]Row{first}, v.Rows...)
	first = cloneRow(v.Commercial[0])
	first["id"] = 1
	v.Commercial = append([]Row{first}, v.Commercial...)
	var out bytes.Buffer
	if e := a.tpl.ExecuteTemplate(&out, "layout", v); e != nil {
		t.Fatal(e)
	}
	for _, old := range []string{`href="/energies/edit/1"`, `href="/energies/edit-commercial/1"`} {
		if strings.Contains(out.String(), old) {
			t.Fatalf("historical edit icon visible: %s", old)
		}
	}
	for _, latest := range []string{`href="/energies/edit/2"`, `href="/energies/edit-commercial/2"`} {
		if strings.Count(out.String(), latest) != 1 {
			t.Fatalf("expected one latest edit icon: %s", latest)
		}
	}
}
