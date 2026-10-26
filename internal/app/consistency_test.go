package app

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func residentialSequence() []Row {
	return []Row{
		{"id": 10, "counter_1": 100, "counter_2": 100, "counter_3": 100, "bill_amount": 300},
		{"id": 20, "counter_1": 200, "counter_2": 200, "counter_3": 200, "bill_amount": 300, "amount_1": 100, "amount_2": 100, "amount_3": 100},
		{"id": 30, "counter_1": 300, "counter_2": 300, "counter_3": 300, "bill_amount": 350, "amount_1": 116.67, "amount_2": 116.67, "amount_3": 116.67},
		{"id": 40, "counter_1": 400, "counter_2": 400, "counter_3": 400, "bill_amount": 300, "amount_1": 100, "amount_2": 100, "amount_3": 100},
	}
}
func assertAmounts(t *testing.T, r Row, want ...float64) {
	t.Helper()
	for i, key := range []string{"amount_1", "amount_2", "amount_3"} {
		if i >= len(want) {
			break
		}
		if r[key] == nil || math.Abs(num(r[key])-want[i]) > 1e-8 {
			t.Fatalf("%s: got %v, want %v", key, r[key], want[i])
		}
	}
}

func TestZeroResidentialConsumptionClearsStaleAmounts(t *testing.T) {
	previous := Row{"counter_1": 100, "counter_2": 100, "counter_3": 100}
	for _, bill := range []any{300.0, 0.0, nil} {
		current := Row{"counter_1": 100, "counter_2": 100, "counter_3": 100, "bill_amount": bill, "amount_1": 999, "amount_2": 999, "amount_3": 999}
		if e := calculateEnergy("energies", current, previous); e != nil {
			t.Fatal(e)
		}
		if bill == 0.0 {
			assertAmounts(t, current, 0, 0, 0)
		} else {
			for _, k := range derivedEnergyFields("energies") {
				if current[k] != nil {
					t.Fatalf("stale charge preserved: %v", current)
				}
			}
		}
		if bill == 300.0 && allocationWarning(current) == "" {
			t.Fatal("unallocated positive bill must be explained")
		}
		if current["bill_amount"] != bill {
			t.Fatal("source bill must never be discarded")
		}
	}
}

func TestIPCAIgnoresUnrelatedSnapshotsAndTracksRealChanges(t *testing.T) {
	base := Row{"id": 1, "created_at": time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), "rental_value": 1000, "tenant_name": "Maria", "tenant_cpf_cnpj": "12345678901"}
	snapshot := func(id int, changes Row) Row {
		r := cloneRow(base)
		r["id"] = id
		r["created_at"] = base["created_at"].(time.Time).AddDate(id, 0, 0)
		for k, v := range changes {
			r[k] = v
		}
		return r
	}
	cases := []struct {
		name string
		logs []Row
		want any
	}{
		{"address notes day", []Row{base, snapshot(2, Row{"address": "Nova rua", "observation": "Nota", "payment_day": "20"})}, 1},
		{"same document renamed", []Row{base, snapshot(2, Row{"tenant_name": "Maria Silva", "tenant_cpf_cnpj": "123.456.789-01"})}, 1},
		{"rent changed", []Row{base, snapshot(2, Row{"rental_value": 1500}), snapshot(3, Row{"rental_value": 1500, "address": "Outra rua"})}, 2},
		{"tenant changed", []Row{base, snapshot(2, Row{"tenant_name": "João", "tenant_cpf_cnpj": "98765432100"})}, 2},
		{"vacancy", []Row{base, snapshot(2, Row{"tenant_name": nil, "tenant_cpf_cnpj": nil})}, 2},
		{"same tenant after vacancy", []Row{base, snapshot(2, Row{"tenant_name": nil, "tenant_cpf_cnpj": nil}), snapshot(3, Row{})}, 3},
		{"rent changed back", []Row{base, snapshot(2, Row{"rental_value": 1500}), snapshot(3, Row{})}, 3},
		{"value cleared", []Row{base, snapshot(2, Row{"rental_value": nil})}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mostRecentRelevantLog(c.logs)
			if c.want == nil {
				if got != nil {
					t.Fatal("unset rental value must not use a past value")
				}
				return
			}
			if got["id"] != c.want {
				t.Fatalf("got reference %v, want %v", got, c.want)
			}
		})
	}
	withoutCPF := cloneRow(base)
	withoutCPF["tenant_cpf_cnpj"] = nil
	if got := mostRecentRelevantLog([]Row{snapshot(2, Row{}), withoutCPF}); got["id"] != 1 {
		t.Fatal("adding CPF to the same name must not reset rent")
	}
}

func TestTenantDeletionRecordsVacancyTransactionally(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM `tb_tenants` WHERE id = \\?").WithArgs("8").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(8))
	m.ExpectQuery("SELECT \\* FROM tb_properties WHERE tenant_id=\\? ORDER BY id FOR UPDATE").WithArgs("8").WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "rental_value", "property_type"}).AddRow(16, 8, 1000, "APARTAMENTO"))
	m.ExpectExec("UPDATE tb_properties SET tenant_id=NULL WHERE tenant_id=\\?").WithArgs("8").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectExec("INSERT INTO `tb_properties_logs` \\(`address`,`complement`,`created_at`,`number`,`observation`,`payment_day`,`property_id`,`property_type`,`rental_value`\\)").WithArgs(nil, nil, sqlmock.AnyArg(), nil, nil, nil, int64(16), "APARTAMENTO", int64(1000)).WillReturnResult(sqlmock.NewResult(17, 1))
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

func TestOnlyLatestReadingCanBeEdited(t *testing.T) {
	for _, key := range []string{"energies", "commercial"} {
		rows := residentialSequence()
		for _, id := range []string{"10", "20", "30"} {
			if _, e := energyPlan(key, id, Row{}, rows); !errors.Is(e, ErrHistoricalReading) {
				t.Fatalf("%s/%s: %v", key, id, e)
			}
		}
	}
	rows := residentialSequence()
	plan, e := energyPlan("energies", "40", Row{"counter_1": 350, "counter_2": 400, "counter_3": 400, "bill_amount": 300}, rows)
	if e != nil {
		t.Fatal(e)
	}
	if len(plan) != 1 || plan[0].ID != "40" {
		t.Fatal("only latest bill should be updated")
	}
	assertAmounts(t, plan[0].Values, 60, 120, 120)
	plan, e = energyPlan("commercial", "2", Row{"internal_counter": 125, "account_consumption": 100, "account_value": 200}, []Row{{"id": 1, "internal_counter": 100}, {"id": 2, "internal_counter": 150}})
	if e != nil {
		t.Fatal(e)
	}
	assertAmounts(t, plan[0].Values, 50, 150)
	first, e := energyPlan("energies", "10", Row{"counter_1": 50, "counter_2": 100, "counter_3": 100, "bill_amount": 300}, rows[:1])
	if e != nil || len(first) != 1 || first[0].Values["amount_1"] != nil {
		t.Fatal("single initial reading must remain editable")
	}
	fresh, e := energyPlan("energies", "", Row{"counter_1": 500, "counter_2": 500, "counter_3": 500, "bill_amount": 300}, rows)
	if e != nil {
		t.Fatal(e)
	}
	assertAmounts(t, fresh[0].Values, 100, 100, 100)
	if _, e = energyPlan("energies", "999", Row{}, rows); e != sql.ErrNoRows {
		t.Fatal("missing reading accepted")
	}
}

func TestHistoricalSaveRollsBackWithoutWriting(t *testing.T) {
	for _, key := range []string{"energies", "commercial"} {
		t.Run(key, func(t *testing.T) {
			a, m := testApp(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT \\* FROM `" + specs[key].Table + "` ORDER BY id ASC FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
			m.ExpectRollback()
			f := url.Values{"id": {"1"}, "counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}, "internalCounter": {"100"}, "accountConsumption": {"100"}, "accountValue": {"200"}}
			if e := a.save(context.Background(), specs[key], f); !errors.Is(e, ErrHistoricalReading) {
				t.Fatalf("old or stale form accepted: %v", e)
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestLatestReadingSaveCommitsOnlyOneUpdate(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	data := sqlmock.NewRows([]string{"id", "counter_1", "counter_2", "counter_3", "bill_amount"})
	for _, r := range residentialSequence() {
		data.AddRow(r["id"], r["counter_1"], r["counter_2"], r["counter_3"], r["bill_amount"])
	}
	m.ExpectQuery("SELECT \\* FROM `tb_energy` ORDER BY id ASC FOR UPDATE").WillReturnRows(data)
	m.ExpectExec("UPDATE `tb_energy` SET").WithArgs(60.0, 120.0, 120.0, 300.0, 350.0, 400.0, 400.0, nil, nil, "40").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit()
	if e := a.save(context.Background(), specs["energies"], url.Values{"id": {"40"}, "counter1": {"350"}, "counter2": {"400"}, "counter3": {"400"}, "billAmount": {"300"}}); e != nil {
		t.Fatal(e)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestHistoricalEditFormIsForbidden(t *testing.T) {
	for _, key := range []string{"energies", "commercial"} {
		a, m := testApp(t)
		m.ExpectQuery("SELECT id FROM `" + specs[key].Table + "` ORDER BY id DESC LIMIT 1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(2))
		w := httptest.NewRecorder()
		a.form(w, httptest.NewRequest("GET", "/energies/edit/1", nil), view{}, specs[key], "1", false)
		if w.Code != 403 {
			t.Fatalf("%s old form: %d", key, w.Code)
		}
		if e := m.ExpectationsWereMet(); e != nil {
			t.Fatal(e)
		}
	}
}

func TestHistoricalPostIsForbiddenWithoutWrites(t *testing.T) {
	for _, key := range []string{"energies", "commercial"} {
		t.Run(key, func(t *testing.T) {
			a, m := testApp(t)
			a.sessions["admin"] = session{UserID: "1", CSRF: "token", Expires: time.Now().Add(time.Hour)}
			m.ExpectQuery("SELECT \\* FROM `tb_users` WHERE id = \\?").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow(1, "ADMIN"))
			m.ExpectBegin()
			m.ExpectQuery("SELECT \\* FROM `" + specs[key].Table + "` ORDER BY id ASC FOR UPDATE").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1).AddRow(2))
			m.ExpectRollback()
			f := url.Values{"_csrf": {"token"}, "id": {"1"}, "counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}, "internalCounter": {"100"}, "accountConsumption": {"100"}, "accountValue": {"200"}}
			path := "/energies/save"
			if key == "commercial" {
				path = "/energies/save-commercial"
			}
			r := httptest.NewRequest("POST", path, strings.NewReader(f.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "admin"})
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 403 || !strings.Contains(w.Body.String(), ErrHistoricalReading.Error()) {
				t.Fatalf("historical POST: %d %s", w.Code, w.Body.String())
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
