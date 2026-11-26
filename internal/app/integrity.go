package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrConflict = errors.New("Este registro foi alterado por outra pessoa. Copie suas alterações e reabra a edição antes de salvar.")
var ErrLastAdmin = errors.New("É necessário manter pelo menos um administrador no sistema.")

func recordVersion(row Row, contacts []Row) string {
	// Normalize nil/empty lists, which are equivalent in a form.
	if len(contacts) == 0 {
		contacts = nil
	}
	b, err := json.Marshal([]any{row, contacts})
	if err != nil {
		panic(err)
	} // Rows contain only database scalar values.
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func lockedRecord(ctx context.Context, tx *sql.Tx, table, id string) (Row, error) {
	rows, err := query(ctx, tx, "SELECT * FROM "+ident(table)+" WHERE id = ? FOR UPDATE", id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return rows[0], nil
}
func checkVersion(ctx context.Context, tx *sql.Tx, s Spec, row Row, want string) error {
	if want == "" {
		return nil
	} // Internal imports/tests; HTTP edits require a version.
	var contacts []Row
	if s.Key == "tenants" || s.Key == "landlords" {
		owner := "tenant_id"
		if s.Key == "landlords" {
			owner = "landlord_id"
		}
		var err error
		contacts, err = query(ctx, tx, "SELECT * FROM tb_contacts WHERE "+owner+"=? ORDER BY id FOR UPDATE", row["id"])
		if err != nil {
			return err
		}
	}
	if recordVersion(row, contacts) != want {
		return ErrConflict
	}
	return nil
}

// Lock all user IDs in a stable order before changing/deleting any user. This
// serializes concurrent demotions without requiring a new table or column.
func protectAdmin(ctx context.Context, tx *sql.Tx, id, nextRole string) error {
	rows, err := query(ctx, tx, "SELECT id,role FROM tb_users ORDER BY id FOR UPDATE")
	if err != nil {
		return err
	}
	admins := 0
	targetAdmin := false
	for _, r := range rows {
		if str(r["role"]) == "ADMIN" {
			admins++
			if str(r["id"]) == id {
				targetAdmin = true
			}
		}
	}
	if targetAdmin && admins <= 1 && nextRole != "ADMIN" {
		return ErrLastAdmin
	}
	return nil
}

func validateDomain(s Spec, v Row) error {
	for _, field := range s.Fields {
		raw := str(v[field.Column])
		if field.Kind != "password" && field.Kind != "number" && utf8.RuneCountInString(raw) > 255 {
			return fmt.Errorf("%s deve ter até 255 caracteres", field.Label)
		}
	}
	if s.Key == "properties" {
		if v["rental_value"] != nil && num(v["rental_value"]) < 0 {
			return errors.New("valor do aluguel não pode ser negativo")
		}
		if day := str(v["payment_day"]); day != "" {
			n, e := strconv.Atoi(day)
			if e != nil || n < 1 || n > 31 {
				return errors.New("dia de pagamento deve estar entre 1 e 31")
			}
		}
	}
	if s.Key == "tenants" || s.Key == "landlords" {
		if raw := str(v["cpf_cnpj"]); raw != "" {
			digits, err := documentDigits(raw)
			if err != nil || (len(digits) != 11 && len(digits) != 14) {
				return errors.New("CPF/CNPJ deve conter 11 ou 14 dígitos")
			}
			// Preserve the stored representation for compatibility with existing data.
		}
		if email := str(v["email"]); email != "" {
			parsed, e := mail.ParseAddress(email)
			if e != nil || parsed.Address != email {
				return errors.New("e-mail inválido")
			}
		}
	}
	return nil
}
func documentDigits(raw string) (string, error) {
	var b strings.Builder
	for _, c := range raw {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '.' || c == '/' || c == '-' || c == ' ':
		default:
			return "", errors.New("documento inválido")
		}
	}
	return b.String(), nil
}
func uniqueDocument(ctx context.Context, tx *sql.Tx, s Spec, id string, v Row) error {
	if v["cpf_cnpj"] == nil {
		return nil
	}
	digits, _ := documentDigits(str(v["cpf_cnpj"]))
	// Recognize formatted legacy values without rewriting them. Locking reads
	// also serialize competing inserts under the transaction's isolation level.
	rows, err := query(ctx, tx, "SELECT id FROM "+ident(s.Table)+" WHERE REPLACE(REPLACE(REPLACE(REPLACE(cpf_cnpj,'.',''),'/',''),'-',''),' ','')=? AND id<>? FOR UPDATE", digits, zeroID(id))
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return errors.New("Já existe um registro com esse CPF/CNPJ.")
	}
	return nil
}
func zeroID(id string) string {
	if id == "" {
		return "0"
	}
	return id
}

// MySQL may abort one participant when concurrent serializable writers meet.
// Retry only whole transactions that have already rolled back, never ambiguous
// commit/network failures, for which repeating a write could duplicate it.
func (a *App) save(ctx context.Context, s Spec, f url.Values) error {
	for attempt := 0; ; attempt++ {
		err := a.saveOnce(ctx, s, f)
		var dbErr *mysql.MySQLError
		if attempt >= 2 || !errors.As(err, &dbErr) || (dbErr.Number != 1213 && dbErr.Number != 1205) {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func validateContacts(f url.Values) error {
	for key := range f {
		rest, ok := strings.CutPrefix(key, "contacts[")
		if !ok {
			continue
		}
		index, tail, ok := strings.Cut(rest, "]")
		n, err := strconv.Atoi(index)
		if !ok || err != nil || n < 0 || n >= 1000 || (tail != ".contact" && tail != ".type" && tail != ".id") {
			return errors.New("índice de contato inválido; são permitidos até 1000 contatos")
		}
		if tail != ".contact" {
			continue
		}
		value := strings.TrimSpace(f.Get(key))
		if value == "" {
			continue
		}
		if utf8.RuneCountInString(value) > 255 {
			return errors.New("contato deve ter até 255 caracteres")
		}
		kind := f.Get("contacts[" + index + "].type")
		if kind != "CELULAR" && kind != "TELEFONE" && kind != "EMAIL" {
			return errors.New("tipo de contato inválido")
		}
		if kind == "EMAIL" {
			parsed, err := mail.ParseAddress(value)
			if err != nil || parsed.Address != value {
				return errors.New("e-mail de contato inválido")
			}
		}
	}
	return nil
}
