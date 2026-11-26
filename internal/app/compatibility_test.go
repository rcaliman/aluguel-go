package app

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
)

func TestProductionWithoutOptionalEmailColumn(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.ExpectQuery("SELECT email FROM tb_tenants LIMIT 0").WillReturnError(&mysql.MySQLError{Number: 1054, Message: "Unknown column email"})
	a, err := New(db, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if a.tenantEmailAvailable {
		t.Fatal("legacy schema not recognized")
	}
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO `tb_tenants`").WithArgs(nil, nil, nil, sqlmock.AnyArg(), nil, nil, nil, nil, "Maria", nil, nil).WillReturnResult(sqlmock.NewResult(1, 1))
	m.ExpectExec("DELETE FROM tb_contacts WHERE tenant_id=\\?").WithArgs("1").WillReturnResult(sqlmock.NewResult(0, 0))
	m.ExpectCommit()
	// The exact argument count excludes the optional email column.
	err = a.save(context.Background(), specs["tenants"], url.Values{"name": {"Maria"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.save(context.Background(), specs["tenants"], url.Values{"name": {"Maria"}, "email": {"maria@example.com"}}); err == nil || !strings.Contains(err.Error(), "não está habilitado") {
		t.Fatalf("email silently lost: %v", err)
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}

func TestSchemaCheckRefusesNonTransactionalTablesWithoutChangingThem(t *testing.T) {
	db, m, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m.ExpectQuery("SELECT TABLE_NAME,ENGINE FROM information_schema.TABLES").WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "ENGINE"}).AddRow("tb_users", "MyISAM"))
	if err := ValidateSchema(context.Background(), db); err == nil || !strings.Contains(err.Error(), "InnoDB") {
		t.Fatalf("unsafe engine accepted: %v", err)
	}
	if err := m.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
