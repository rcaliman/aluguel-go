package app

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

//go:embed schema.json
var schemaJSON []byte

type Row map[string]any
type Field struct {
	Name, Column, Label, Kind string
	Required, Derived         bool
}
type Spec struct {
	Key, Table, Title string
	Timestamps        bool
	Fields            []Field
}

var specs map[string]Spec

func init() {
	var ss []Spec
	if e := json.Unmarshal(schemaJSON, &ss); e != nil {
		panic(e)
	}
	specs = map[string]Spec{}
	for _, s := range ss {
		specs[s.Key] = s
	}
}
func str(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.Format("2006-01-02 15:04:05")
	default:
		return fmt.Sprint(v)
	}
}
func num(v any) float64     { n, _ := strconv.ParseFloat(str(v), 64); return n }
func ident(s string) string { return "`" + s + "`" }

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func query(ctx context.Context, q queryer, s string, args ...any) ([]Row, error) {
	rows, e := q.QueryContext(ctx, s, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	cols, e := rows.Columns()
	if e != nil {
		return nil, e
	}
	columnTypes, e := rows.ColumnTypes()
	if e != nil {
		return nil, e
	}
	out := []Row{}
	for rows.Next() {
		v := make([]any, len(cols))
		p := make([]any, len(cols))
		for i := range v {
			p[i] = &v[i]
		}
		if e = rows.Scan(p...); e != nil {
			return nil, e
		}
		r := Row{}
		for i, c := range cols {
			if b, ok := v[i].([]byte); ok {
				if c == "main" && len(b) == 1 && (b[0] == 0 || b[0] == 1) {
					v[i] = b[0] == 1
				} else {
					v[i] = string(b)
				}
			}
			if t, ok := v[i].(time.Time); ok && columnTypes[i].DatabaseTypeName() == "DATE" {
				v[i] = t.Format("2006-01-02")
			}
			r[c] = v[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func one(ctx context.Context, q queryer, table string, id any) (Row, error) {
	rs, e := query(ctx, q, "SELECT * FROM "+ident(table)+" WHERE id = ?", id)
	if e != nil {
		return nil, e
	}
	if len(rs) == 0 {
		return nil, sql.ErrNoRows
	}
	return rs[0], nil
}
func insert(ctx context.Context, tx *sql.Tx, table string, v Row) (int64, error) {
	cols, args, marks := []string{}, []any{}, []string{}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := v[k]
		cols = append(cols, ident(k))
		args = append(args, x)
		marks = append(marks, "?")
	}
	r, e := tx.ExecContext(ctx, "INSERT INTO "+ident(table)+" ("+strings.Join(cols, ",")+") VALUES ("+strings.Join(marks, ",")+")", args...)
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func update(ctx context.Context, tx *sql.Tx, table string, id any, v Row) error {
	cols, args := []string{}, []any{}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		x := v[k]
		cols = append(cols, ident(k)+"=?")
		args = append(args, x)
	}
	args = append(args, id)
	_, e := tx.ExecContext(ctx, "UPDATE "+ident(table)+" SET "+strings.Join(cols, ",")+" WHERE id=?", args...)
	return e
}
func enumOptions(kind string) [][2]string {
	switch kind {
	case "RoleEnum":
		return [][2]string{{"ADMIN", "administrador"}, {"OPERADOR", "operador"}, {"LEITOR", "leitor"}}
	case "PropertyTypeEnum":
		return [][2]string{{"LOJA", "loja"}, {"SALA_COMERCIAL", "sala comercial"}, {"QUITINETE", "quitinete"}, {"APARTAMENTO", "apartamento"}, {"CONDOMINIO", "condomínio"}}
	case "PropertyUseTypeEnum":
		return [][2]string{{"RESIDENCIAL", "residencial"}, {"COMERCIAL", "comercial"}}
	case "MaritalStatusEnum":
		return [][2]string{{"SOLTEIRO", "solteiro(a)"}, {"CASADO", "casado(a)"}, {"DIVORCIADO", "divorciado(a)"}, {"VIUVO", "viúvo(a)"}, {"UNIAO_ESTAVEL", "união estável"}}
	case "ContactTypeEnum":
		return [][2]string{{"CELULAR", "celular"}, {"EMAIL", "email"}, {"TELEFONE", "telefone"}}
	}
	return nil
}
func display(v any) string {
	s := str(v)
	for _, k := range []string{"RoleEnum", "PropertyTypeEnum", "PropertyUseTypeEnum", "MaritalStatusEnum", "ContactTypeEnum"} {
		for _, o := range enumOptions(k) {
			if o[0] == s {
				return o[1]
			}
		}
	}
	return s
}
func validateFields(s Spec, f url.Values, editing bool) (Row, error) {
	v := Row{}
	for _, c := range s.Fields {
		if c.Derived {
			continue
		}
		raw := strings.TrimSpace(f.Get(c.Name))
		if c.Kind == "password" {
			raw = f.Get(c.Name)
		}
		if c.Kind == "password" && strings.TrimSpace(raw) == "" && editing {
			continue
		}
		if c.Required && strings.TrimSpace(raw) == "" {
			return nil, fmt.Errorf("%s é obrigatório", c.Label)
		}
		var x any = raw
		if raw == "" {
			x = nil
		} else {
			switch c.Kind {
			case "number":
				n, e := strconv.ParseFloat(raw, 64)
				if e != nil || math.IsInf(n, 0) || math.IsNaN(n) {
					return nil, fmt.Errorf("%s inválido", c.Label)
				}
				x = n
			case "date":
				if _, e := time.Parse("2006-01-02", raw); e != nil {
					return nil, fmt.Errorf("%s inválida", c.Label)
				}
			case "tenant":
				n, e := strconv.ParseInt(raw, 10, 64)
				if e != nil || n < 1 {
					return nil, fmt.Errorf("inquilino inválido")
				}
				x = n
			case "password":
				if utf8.RuneCountInString(raw) < 8 || len(raw) > 72 {
					return nil, fmt.Errorf("nova senha deve ter pelo menos 8 caracteres e até 72 bytes")
				}
				h, e := bcrypt.GenerateFromPassword([]byte(raw), 10)
				if e != nil {
					return nil, e
				}
				x = string(h)
			}
		}
		if c.Kind == "checkbox" {
			x = raw == "true" || raw == "on" || raw == "1"
		}
		if opts := enumOptions(c.Kind); len(opts) > 0 && raw != "" {
			ok := false
			for _, o := range opts {
				ok = ok || raw == o[0]
			}
			if !ok {
				return nil, fmt.Errorf("%s inválido", c.Label)
			}
		}
		v[c.Column] = x
	}
	if s.Key == "tenants" || s.Key == "landlords" {
		if err := validateContacts(f); err != nil {
			return nil, err
		}
	}
	if err := validateDomain(s, v); err != nil {
		return nil, err
	}
	return v, nil
}
func (a *App) saveOnce(ctx context.Context, s Spec, f url.Values) (err error) {
	if s.Key == "titles" {
		return a.saveEnergyTitles(ctx, f)
	}
	id := f.Get("id")
	editing := id != ""
	if editing {
		n, e := ParseID(id)
		if e != nil {
			return e
		}
		id = str(n)
	}
	v, e := validateFields(s, f, editing)
	if e != nil {
		return e
	}
	if s.Key == "tenants" && !a.tenantEmailAvailable {
		if v["email"] != nil {
			return errors.New("O campo de e-mail ainda não está habilitado neste banco. Cadastre o e-mail como contato ou habilite a coluna opcional conforme o README.")
		}
		delete(v, "email")
	}
	var opts *sql.TxOptions
	if s.Key == "energies" || s.Key == "commercial" || v["cpf_cnpj"] != nil {
		opts = &sql.TxOptions{Isolation: sql.LevelSerializable}
	}
	tx, e := a.db.BeginTx(ctx, opts)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if s.Key == "energies" || s.Key == "commercial" {
		if e = saveEnergy(ctx, tx, s, id, v, f.Get("_version")); e != nil {
			return e
		}
		return tx.Commit()
	}
	if s.Key == "users" && editing {
		if e = protectAdmin(ctx, tx, id, str(v["role"])); e != nil {
			return e
		}
	}
	if editing {
		current, err := lockedRecord(ctx, tx, s.Table, id)
		if err != nil {
			return err
		}
		if err = checkVersion(ctx, tx, s, current, f.Get("_version")); err != nil {
			return err
		}
	}
	if s.Key == "tenants" || s.Key == "landlords" {
		if e = uniqueDocument(ctx, tx, s, id, v); e != nil {
			return e
		}
	}
	if s.Key == "landlords" && v["main"] == true {
		if _, e = tx.ExecContext(ctx, "UPDATE tb_landlords SET main=false"); e != nil {
			return e
		}
	}

	now := a.now()
	if s.Timestamps {
		if editing {
			v["updated_at"] = now
		} else {
			v["created_at"] = now
		}
	}
	if editing {
		e = update(ctx, tx, s.Table, id, v)
	} else {
		var n int64
		n, e = insert(ctx, tx, s.Table, v)
		id = str(n)
	}
	if e != nil {
		return e
	}
	if s.Key == "tenants" || s.Key == "landlords" {
		owner := "tenant_id"
		if s.Key == "landlords" {
			owner = "landlord_id"
		}
		if _, e = tx.ExecContext(ctx, "DELETE FROM tb_contacts WHERE "+owner+"=?", id); e != nil {
			return e
		}
		for i := 0; i < 1000; i++ {
			base := fmt.Sprintf("contacts[%d].", i)
			if !f.Has(base + "contact") {
				continue
			}
			c := strings.TrimSpace(f.Get(base + "contact"))
			if c == "" {
				continue
			}
			typ := f.Get(base + "type")
			if typ != "CELULAR" && typ != "TELEFONE" && typ != "EMAIL" {
				return fmt.Errorf("tipo de contato inválido")
			}
			if _, e = insert(ctx, tx, "tb_contacts", Row{owner: id, "contact": c, "contact_type": typ, "created_at": now}); e != nil {
				return e
			}
		}
	}
	if s.Key == "properties" {
		p := cloneRow(v)
		p["id"] = id
		if e = appendPropertyLog(ctx, tx, p, now); e != nil {
			return e
		}
	}

	if e = tx.Commit(); e != nil {
		return e
	}
	if s.Key == "users" && editing {
		a.revokeSessions(id)
	}
	return nil
}
func calculateEnergy(key string, v, p Row) error {
	if key == "energies" {
		for _, k := range derivedEnergyFields(key) {
			v[k] = nil
		}
		if p == nil {
			return nil
		}
		cons := []float64{}
		total := 0.0
		for _, k := range []string{"counter_1", "counter_2", "counter_3"} {
			c := num(v[k])
			if c >= num(p[k]) {
				c -= num(p[k])
			}
			cons = append(cons, c)
			total += c
		}
		if total > 0 {
			for i, c := range cons {
				v[fmt.Sprintf("amount_%d", i+1)] = c / total * num(v["bill_amount"])
			}
		} else if v["bill_amount"] != nil && num(v["bill_amount"]) == 0 {
			for _, k := range derivedEnergyFields(key) {
				v[k] = float64(0)
			}
		}
		return nil
	}
	for _, k := range []string{"amount_1", "amount_2", "calculated_consumption_1", "calculated_consumption_2"} {
		v[k] = nil
	}
	if p == nil {
		return nil
	}
	total := num(v["account_consumption"])
	if total <= 0 {
		return errors.New("consumo da conta deve ser maior que zero")
	}
	c1 := num(v["internal_counter"]) - num(p["internal_counter"])
	c2 := total - c1
	v["calculated_consumption_1"] = c1
	v["calculated_consumption_2"] = c2
	v["amount_1"] = c1 / total * num(v["account_value"])
	v["amount_2"] = c2 / total * num(v["account_value"])
	return nil
}
func (a *App) delete(ctx context.Context, s Spec, id string, u Row) error {
	tx, e := a.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if s.Key == "users" {
		if e = protectAdmin(ctx, tx, id, ""); e != nil {
			return e
		}
	}
	r, e := lockedRecord(ctx, tx, s.Table, id)
	if e != nil {
		return e
	}
	if s.Key == "users" && str(r["id"]) == str(u["id"]) {
		return errors.New("o usuário conectado não pode ser excluído")
	}
	switch s.Key {
	case "tenants":
		properties, err := query(ctx, tx, "SELECT * FROM tb_properties WHERE tenant_id=? ORDER BY id FOR UPDATE", id)
		if err != nil {
			return err
		}
		_, e = tx.ExecContext(ctx, "UPDATE tb_properties SET tenant_id=NULL WHERE tenant_id=?", id)
		if e == nil {
			for _, p := range properties {
				p["tenant_id"] = nil
				if e = appendPropertyLog(ctx, tx, p, a.now()); e != nil {
					break
				}
			}
		}
		if e == nil {
			_, e = tx.ExecContext(ctx, "DELETE FROM tb_contacts WHERE tenant_id=?", id)
		}
	case "landlords":
		_, e = tx.ExecContext(ctx, "DELETE FROM tb_contacts WHERE landlord_id=?", id)
	case "properties":
		_, e = tx.ExecContext(ctx, "DELETE FROM tb_properties_logs WHERE property_id=?", id)
	}
	if e != nil {
		return e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM "+ident(s.Table)+" WHERE id=?", id); e != nil {
		return e
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	if s.Key == "users" {
		a.revokeSessions(id)
	}
	return nil
}

func (a *App) now() time.Time {
	loc, e := time.LoadLocation(a.cfg.Database.Timezone)
	if e != nil {
		loc = time.UTC
	}
	return time.Now().In(loc)
}

func appendPropertyLog(ctx context.Context, tx *sql.Tx, p Row, now time.Time) error {
	log := Row{"property_id": p["id"], "created_at": now}
	for _, k := range []string{"property_type", "number", "address", "rental_value", "complement", "observation", "payment_day"} {
		log[k] = p[k]
	}
	if p["tenant_id"] != nil {
		t, e := one(ctx, tx, "tb_tenants", p["tenant_id"])
		if e != nil {
			return e
		}
		log["tenant_name"] = t["name"]
		log["tenant_cpf_cnpj"] = t["cpf_cnpj"]
	}
	_, e := insert(ctx, tx, "tb_properties_logs", log)
	return e
}
