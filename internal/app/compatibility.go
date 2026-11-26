package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/go-sql-driver/mysql"
)

// Email was added to the form separately from this hardening release. Treat
// that column as optional so an older production database keeps working.
func tenantEmailColumn(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT email FROM tb_tenants LIMIT 0")
	if err == nil {
		return true, rows.Close()
	}
	var dbErr *mysql.MySQLError
	if errors.As(err, &dbErr) && dbErr.Number == 1054 {
		return false, nil
	}
	return false, err
}
func checkOptionalSchema(ctx context.Context, db *sql.DB) error {
	available, err := tenantEmailColumn(ctx, db)
	if err != nil {
		return err
	}
	if !available {
		log.Print("Compatibilidade: tb_tenants.email ausente; cadastro continua disponível e e-mails podem ser registrados como contatos. Consulte README para habilitar o campo opcional.")
	}
	return nil
}

func checkTransactionalTables(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT TABLE_NAME,ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=DATABASE()")
	if err != nil {
		return err
	}
	defer rows.Close()
	required := map[string]bool{"tb_contacts": true, "tb_properties_logs": true}
	for _, s := range specs {
		required[s.Table] = true
	}
	for rows.Next() {
		var name string
		var engine sql.NullString
		if err := rows.Scan(&name, &engine); err != nil {
			return err
		}
		if required[name] && engine.String != "InnoDB" {
			return fmt.Errorf("tabela %s usa %s: InnoDB é necessário para transações e bloqueios; nenhuma conversão foi executada", name, engine.String)
		}
	}
	return rows.Err()
}
