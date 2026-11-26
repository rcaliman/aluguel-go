//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// This suite only creates fixtures in an explicitly configured EMPTY database
// whose name ends in _test. It never uses config.json or production credentials.
func TestMySQLCompatibility(t *testing.T) {
	dsn := os.Getenv("ALUGUEL_TEST_DSN")
	if dsn == "" {
		t.Skip("ALUGUEL_TEST_DSN não definido")
	}
	c, e := mysql.ParseDSN(dsn)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasSuffix(c.DBName, "_test") {
		t.Fatal("o banco de integração deve terminar em _test")
	}
	c.ParseTime = true
	c.Loc = time.UTC
	db, e := sql.Open("mysql", c.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	var count int
	if e = db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=?", c.DBName).Scan(&count); e != nil {
		t.Fatal(e)
	}
	if count != 0 {
		t.Fatal("banco de integração deve estar vazio")
	}
	fixture, e := os.ReadFile("testdata/mysql.sql")
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range strings.Split(string(fixture), ";") {
		if strings.TrimSpace(s) != "" {
			if _, e = db.Exec(s); e != nil {
				t.Fatal(e)
			}
		}
	}
	defer func() {
		for _, table := range []string{"tb_contacts", "tb_properties_logs", "tb_properties", "tb_tenants", "tb_landlords", "tb_users", "tb_energy", "tb_commercial_energy", "tb_energy_title"} {
			if _, e := db.Exec("DROP TABLE IF EXISTS " + table); e != nil {
				t.Error(e)
			}
		}
	}()
	if e = ValidateSchema(ctx, db); e != nil {
		t.Fatal(e)
	}
	a, e := New(db, Config{SessionHours: 12, Database: DatabaseConfig{Timezone: "America/Sao_Paulo"}})
	if e != nil {
		t.Fatal(e)
	}
	save := func(key string, f url.Values) {
		t.Helper()
		if e := a.save(ctx, specs[key], f); e != nil {
			t.Fatalf("save %s: %v", key, e)
		}
	}
	save("users", url.Values{"username": {"admin-test"}, "password": {"senha-existente"}, "role": {"ADMIN"}})
	u, e := one(ctx, db, "tb_users", 1)
	if e != nil {
		t.Fatal(e)
	}
	if e = bcrypt.CompareHashAndPassword([]byte(str(u["password"])), []byte("senha-existente")); e != nil {
		t.Fatal(e)
	}
	var physicalTime string
	if e = db.QueryRow("SELECT DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') FROM tb_users WHERE id=1").Scan(&physicalTime); e != nil {
		t.Fatal(e)
	}
	stored, e := time.Parse("2006-01-02 15:04:05", physicalTime)
	if e != nil || time.Since(stored).Abs() > time.Minute {
		t.Fatalf("Instant must be persisted in UTC: %s (%v)", physicalTime, e)
	}
	save("landlords", url.Values{"name": {"Locador 1"}, "main": {"true"}})
	save("landlords", url.Values{"name": {"Locador 2"}, "main": {"true"}})
	if e = db.QueryRow("SELECT COUNT(*) FROM tb_landlords WHERE main=1").Scan(&count); e != nil || count != 1 {
		t.Fatalf("main: %d %v", count, e)
	}
	save("tenants", url.Values{"name": {"José <teste>"}, "cpfCnpj": {"12345678901"}, "dateOfBirth": {"1980-02-29"}, "contacts[0].type": {"CELULAR"}, "contacts[0].contact": {"27999999999"}})
	save("properties", url.Values{"propertyType": {"APARTAMENTO"}, "useType": {"RESIDENCIAL"}, "tenant": {"1"}, "value": {"1234.56"}, "paymentDay": {"10"}, "number": {"101"}})
	var tenant string
	if e = db.QueryRow("SELECT tenant_name FROM tb_properties_logs WHERE property_id=1").Scan(&tenant); e != nil || tenant != "José <teste>" {
		t.Fatalf("history: %s %v", tenant, e)
	}
	save("tenants", url.Values{"id": {"1"}, "name": {"José atualizado"}, "contacts[0].type": {"EMAIL"}, "contacts[0].contact": {"jose@example.com"}})
	if e = db.QueryRow("SELECT COUNT(*) FROM tb_contacts WHERE tenant_id=1").Scan(&count); e != nil || count != 1 {
		t.Fatalf("contacts %d %v", count, e)
	}
	save("energies", url.Values{"counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}, "date": {"2026-09-10"}})
	save("energies", url.Values{"counter1": {"200"}, "counter2": {"300"}, "counter3": {"400"}, "billAmount": {"600"}, "date": {"2026-10-10"}})
	energy, e := one(ctx, db, "tb_energy", 2)
	if e != nil || num(energy["amount_1"]) != 100 || num(energy["amount_3"]) != 300 {
		t.Fatalf("energy %v %v", energy, e)
	}
	save("commercial", url.Values{"internalCounter": {"100"}, "accountConsumption": {"100"}, "accountValue": {"200"}})
	save("commercial", url.Values{"internalCounter": {"150"}, "accountConsumption": {"200"}, "accountValue": {"400"}})
	energy, e = one(ctx, db, "tb_commercial_energy", 2)
	if e != nil || num(energy["amount_1"]) != 100 || num(energy["amount_2"]) != 300 {
		t.Fatalf("commercial %v %v", energy, e)
	}

	// Historical writes are refused; only the last ID of each table is editable.
	save("energies", url.Values{"counter1": {"300"}, "counter2": {"400"}, "counter3": {"500"}, "billAmount": {"300"}})
	if err := a.save(ctx, specs["energies"], url.Values{"id": {"2"}, "counter1": {"150"}, "counter2": {"300"}, "counter3": {"400"}, "billAmount": {"600"}}); !errors.Is(err, ErrHistoricalReading) {
		t.Fatalf("historical update accepted: %v", err)
	}
	unchanged, err := one(ctx, db, "tb_energy", 2)
	if err != nil {
		t.Fatal(err)
	}
	assertAmounts(t, unchanged, 100, 200, 300)
	save("energies", url.Values{"id": {"3"}, "counter1": {"200"}, "counter2": {"300"}, "counter3": {"400"}, "billAmount": {"300"}})
	changed, err := one(ctx, db, "tb_energy", 3)
	if err != nil {
		t.Fatal(err)
	}
	if changed["amount_1"] != nil {
		t.Fatal("zero consumption retained stale amount")
	}
	if err = a.save(ctx, specs["commercial"], url.Values{"id": {"1"}, "internalCounter": {"125"}, "accountConsumption": {"100"}, "accountValue": {"200"}}); !errors.Is(err, ErrHistoricalReading) {
		t.Fatalf("historical commercial update accepted: %v", err)
	}
	save("commercial", url.Values{"id": {"2"}, "internalCounter": {"125"}, "accountConsumption": {"200"}, "accountValue": {"400"}})
	changed, err = one(ctx, db, "tb_commercial_energy", 2)
	if err != nil {
		t.Fatal(err)
	}
	assertAmounts(t, changed, 50, 350)

	t.Run("ConcurrentFirstEnergyReadings", func(t *testing.T) {
		// This is the disposable fixture, never the configured application DB.
		if _, err := db.Exec("TRUNCATE TABLE tb_energy"); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		start := make(chan struct{})
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				errs <- a.save(ctx, specs["energies"], url.Values{"counter1": {"100"}, "counter2": {"100"}, "counter3": {"100"}, "billAmount": {"300"}})
			}()
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM tb_energy").Scan(&n); err != nil || n != 2 {
			t.Fatalf("rows=%d err=%v", n, err)
		}
	})
	t.Run("ConcurrentPropertyEdits", func(t *testing.T) {
		before, err := one(ctx, db, "tb_properties", 1)
		if err != nil {
			t.Fatal(err)
		}
		version := recordVersion(before, nil)
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, value := range []string{"1500", "1600"} {
			go func(value string) {
				<-start
				errs <- a.save(ctx, specs["properties"], url.Values{"id": {"1"}, "_version": {version}, "propertyType": {"APARTAMENTO"}, "tenant": {"1"}, "value": {value}, "paymentDay": {"10"}})
			}(value)
		}
		close(start)
		ok, conflict := 0, 0
		for i := 0; i < 2; i++ {
			err := <-errs
			if err == nil {
				ok++
			} else if errors.Is(err, ErrConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if ok != 1 || conflict != 1 {
			t.Fatalf("success=%d conflict=%d", ok, conflict)
		}
	})

	t.Run("ConcurrentLastAdminDemotions", func(t *testing.T) {
		save("users", url.Values{"username": {"second-admin"}, "password": {"second password"}, "role": {"ADMIN"}})
		rows, err := query(ctx, db, "SELECT id,username FROM tb_users ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, row := range rows {
			go func(row Row) {
				<-start
				errs <- a.save(ctx, specs["users"], url.Values{"id": {str(row["id"])}, "username": {str(row["username"])}, "role": {"LEITOR"}})
			}(row)
		}
		close(start)
		ok, rejected := 0, 0
		for range rows {
			err := <-errs
			if err == nil {
				ok++
			} else if errors.Is(err, ErrLastAdmin) {
				rejected++
			} else {
				t.Fatal(err)
			}
		}
		if ok != 1 || rejected != 1 {
			t.Fatalf("success=%d rejected=%d", ok, rejected)
		}
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM tb_users WHERE role='ADMIN'").Scan(&n); err != nil || n != 1 {
			t.Fatalf("admins=%d err=%v", n, err)
		}
	})

	save("titles", url.Values{"titleAmount1": {"Kit 1"}, "titleAmount2": {"Kit 2"}, "titleAmount3": {"Kit 3"}, "titleAmount4": {"Loja 1"}, "titleAmount5": {"Loja 2"}})
	save("titles", url.Values{"titleAmount2": {"Kit alterada"}})
	titles, err := energyTitles(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if titles["title_amount_1"] != "Kit 1" || titles["title_amount_2"] != "Kit alterada" || titles["title_amount_3"] != "Kit 3" || titles["title_amount_4"] != "Loja 1" || titles["title_amount_5"] != "Loja 2" {
		t.Fatalf("partial title update lost other columns: %v", titles)
	}

	if e = a.delete(ctx, specs["tenants"], "1", u); e != nil {
		t.Fatal(e)
	}
	p, e := one(ctx, db, "tb_properties", 1)
	if e != nil || p["tenant_id"] != nil {
		t.Fatalf("tenant detach %v %v", p, e)
	}
	logs, err := query(ctx, db, "SELECT * FROM tb_properties_logs WHERE property_id=1 ORDER BY id DESC")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) < 2 || logs[0]["tenant_name"] != nil || logs[0]["tenant_cpf_cnpj"] != nil {
		t.Fatal("tenant removal must leave a vacancy snapshot")
	}
	if e = a.delete(ctx, specs["properties"], "1", u); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow("SELECT COUNT(*) FROM tb_properties_logs").Scan(&count); e != nil || count != 0 {
		t.Fatalf("history delete %d %v", count, e)
	}
	t.Run("LegacyDatabaseWithoutEmail", func(t *testing.T) {
		if _, err := db.Exec("ALTER TABLE tb_tenants DROP COLUMN email"); err != nil {
			t.Fatal(err)
		}
		if err := ValidateSchema(ctx, db); err != nil {
			t.Fatal(err)
		}
		legacy, err := New(db, a.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := legacy.save(ctx, specs["tenants"], url.Values{"name": {"Legacy tenant"}, "contacts[0].type": {"EMAIL"}, "contacts[0].contact": {"legacy@example.com"}}); err != nil {
			t.Fatal(err)
		}
	})

}
