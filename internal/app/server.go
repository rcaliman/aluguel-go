package app

import (
	"aluguel/web"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type session struct {
	UserID, CSRF string
	Expires      time.Time
}
type App struct {
	db       *sql.DB
	cfg      Config
	tpl      *template.Template
	mu       sync.Mutex
	sessions map[string]session
}
type view struct {
	CorrectionData                                       Row
	SortField                                            string
	Title, Page, CSRF, Error, Key, Action                string
	User, Record, Extra                                  Row
	Spec                                                 Spec
	Rows, Contacts, Tenants, Landlords, Commercial, Logs []Row
	Options                                              map[string][][2]string
	Fields                                               []Field
	Body                                                 template.HTML
	Year, Month                                          int
	Correction                                           string
}

func New(db *sql.DB, c Config) (*App, error) {
	loc := aLocation(c)
	t, e := template.New("").Funcs(template.FuncMap{"isCompany": func(v any) bool { d := document(v); return strings.Contains(d, "/") }, "copies": func() []int { return []int{1, 2} }, "months": func() []string { return months }, "words": words, "doc": document, "lower": strings.ToLower, "upper": strings.ToUpper, "s": func(v any) string {
		if t, ok := v.(time.Time); ok {
			return str(t.In(loc))
		}
		return str(v)
	}, "display": display, "money": money, "allocationWarning": allocationWarning, "eqs": func(a, b any) bool { return str(a) == str(b) }, "get": func(r Row, k string) any { return r[k] }, "canWrite": func(r Row) bool { return r != nil && (r["role"] == "ADMIN" || r["role"] == "OPERADOR") }, "date": func(v any) string {
		if t, ok := v.(time.Time); ok {
			return t.In(loc).Format("2006-01-02")
		}
		s := str(v)
		if len(s) > 10 {
			return s[:10]
		}
		return s
	}, "truth": func(v any) bool { return v == true || str(v) == "1" || str(v) == "true" || str(v) == "on" }, "add": func(a, b int) int { return a + b }}).Funcs(uiFunctions(c)).ParseFS(web.Files, "templates/*.html")
	if e != nil {
		return nil, e
	}
	return &App{db: db, cfg: c, tpl: t, sessions: map[string]session{}}, nil
}
func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (a *App) session(r *http.Request) (string, session) {
	c, e := r.Cookie("ALUGUEL_SESSION")
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, s := range a.sessions {
		if now.After(s.Expires) {
			delete(a.sessions, k)
		}
	}
	if e == nil {
		if s, ok := a.sessions[c.Value]; ok {
			s.Expires = now.Add(a.cfg.sessionTimeout())
			a.sessions[c.Value] = s
			return c.Value, s
		}
	}
	return "", session{}
}
func (a *App) newSession(w http.ResponseWriter, uid string) session {
	s := session{UserID: uid, CSRF: token(), Expires: time.Now().Add(a.cfg.sessionTimeout())}
	id := token()
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "ALUGUEL_SESSION", Value: id, Path: "/", HttpOnly: true, Secure: a.cfg.SecureCookies, SameSite: http.SameSiteLaxMode})
	return s
}
func (a *App) render(w http.ResponseWriter, v view) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	if e := a.tpl.ExecuteTemplate(&b, "layout", v); e != nil {
		log.Printf("template: %v", e)
		http.Error(w, "Erro ao renderizar página", 500)
		return
	}
	fmt.Fprint(w, b.String())
}
func (a *App) fail(w http.ResponseWriter, r *http.Request, e error) {
	code := 500
	msg := "Não foi possível concluir a operação."
	if errors.Is(e, ErrHistoricalReading) {
		code = http.StatusForbidden
		msg = e.Error()
	}
	if errors.Is(e, sql.ErrNoRows) {
		code = 404
		msg = "Registro não encontrado."
	}
	log.Printf("%s %s: %v", r.Method, r.URL.Path, e)
	http.Error(w, msg, code)
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web.Files, "static")
	for _, p := range []string{"/css/", "/js/", "/icons/", "/vendor/", "/favicon.ico", "/manifest.json", "/sw.js"} {
		mux.Handle(p, http.FileServer(http.FS(static)))
	}
	mux.HandleFunc("/", a.serve)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "same-origin")
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		mux.ServeHTTP(w, r)
	})
}
func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	sid, sess := a.session(r)
	if sid == "" {
		sess = a.newSession(w, "")
	}
	var user Row
	if sess.UserID != "" {
		var e error
		user, e = one(ctx, a.db, "tb_users", sess.UserID)
		if e != nil {
			if !errors.Is(e, sql.ErrNoRows) {
				a.fail(w, r, e)
				return
			}
			user = nil
		}
	}
	v := view{Title: "Aluguel de Imóveis", Page: "home", User: user, CSRF: sess.CSRF, Year: a.now().Year(), Month: int(a.now().Month())}
	if r.Method != "GET" && r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	if r.Method == "POST" {
		if e := r.ParseForm(); e != nil {
			http.Error(w, "Formulário inválido", 400)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Form.Get("_csrf")), []byte(sess.CSRF)) != 1 {
			http.Error(w, "Formulário expirado. Atualize a página.", 403)
			return
		}
	}
	p := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(p, "/")
	if p == "login" {
		v.Page = "login"
		if r.Method == "POST" {
			rows, e := query(ctx, a.db, "SELECT * FROM tb_users WHERE username=?", r.Form.Get("username"))
			if e != nil {
				a.fail(w, r, e)
				return
			}
			if len(rows) != 1 || bcrypt.CompareHashAndPassword([]byte(str(rows[0]["password"])), []byte(r.Form.Get("password"))) != nil {
				v.Error = "Usuário ou senha inválidos"
			} else {
				a.mu.Lock()
				delete(a.sessions, sid)
				a.mu.Unlock()
				a.newSession(w, str(rows[0]["id"]))
				http.Redirect(w, r, "/", 303)
				return
			}
		}
		a.render(w, v)
		return
	}
	if p == "" {
		a.render(w, v)
		return
	}
	if user == nil {
		http.Redirect(w, r, "/login", 303)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if p == "logout" && r.Method == "POST" {
		a.mu.Lock()
		delete(a.sessions, sid)
		a.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "ALUGUEL_SESSION", Path: "/", MaxAge: -1, HttpOnly: true})
		http.Redirect(w, r, "/login", 303)
		return
	}
	if strings.HasPrefix(p, "password/") {
		if !((p == "password/form" && r.Method == "GET") || (p == "password/update" && r.Method == "POST")) {
			http.NotFound(w, r)
			return
		}
		a.password(w, r, v)
		return
	}
	if strings.HasPrefix(p, "infopage/") {
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		a.info(w, r, v, parts[len(parts)-1])
		return
	}
	if strings.HasPrefix(p, "receipts/") {
		a.receipts(w, r, v)
		return
	}
	if strings.HasPrefix(p, "contracts/") {
		a.contract(w, r, v)
		return
	}
	key := parts[0]
	action := ""
	id := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if len(parts) > 2 {
		id = parts[2]
	}
	if key == "energies" {
		switch action {
		case "new-commercial":
			key = "commercial"
			action = "new"
		case "edit-commercial":
			key = "commercial"
			action = "edit"
		case "save-commercial":
			key = "commercial"
			action = "save"
		case "titleform":
			key = "titles"
			action = "new"
		case "savetitle":
			key = "titles"
			action = "save"
		}
	}
	spec, ok := specs[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if key == "users" && str(user["role"]) != "ADMIN" {
		http.Error(w, "Acesso negado", 403)
		return
	}
	write := action != "" || r.Method == "POST"
	if write && str(user["role"]) != "ADMIN" && str(user["role"]) != "OPERADOR" {
		http.Error(w, "Acesso negado", 403)
		return
	}
	validPath := (action == "" && len(parts) == 1) || (action == "new" && len(parts) == 2) || (action == "save" && len(parts) == 2)
	if (action == "edit" || action == "delete") && len(parts) == 3 {
		_, err := ParseID(id)
		validPath = err == nil
	}
	if !validPath {
		http.NotFound(w, r)
		return
	}
	if (key == "energies" || key == "commercial") && action == "delete" {
		http.NotFound(w, r)
		return
	}
	v.Key = key
	v.Title = spec.Title
	v.Spec = spec
	v.Fields = spec.Fields
	switch {
	case r.Method == "POST" && (action == "save" || action == "" || action == "savetitle"):
		if e := a.save(ctx, spec, r.Form); e != nil {
			if errors.Is(e, ErrHistoricalReading) {
				a.fail(w, r, e)
				return
			}
			v.Error = validationError(e)
			a.form(w, r, v, spec, r.Form.Get("id"), true)
			return
		}
		http.Redirect(w, r, listPath(key), 303)
	case action == "delete" && r.Method == "POST":
		if e := a.delete(ctx, spec, id, user); e != nil {
			a.fail(w, r, e)
			return
		}
		http.Redirect(w, r, listPath(key), 303)
	case (action == "new" || action == "edit") && r.Method == "GET":
		a.form(w, r, v, spec, id, false)
	case action == "" && r.Method == "GET":
		a.list(w, r, v, spec)
	default:
		http.NotFound(w, r)
	}
}
func validationError(e error) string {
	if strings.Contains(e.Error(), "Duplicate entry") {
		return "Já existe um registro com esse CPF/CNPJ ou usuário."
	}
	if strings.Contains(e.Error(), "Error ") {
		log.Printf("save: %v", e)
		return "Não foi possível salvar. Verifique os dados e os vínculos."
	}
	return e.Error()
}
func listPath(key string) string {
	if key == "commercial" || key == "titles" {
		return "/energies"
	}
	return "/" + key
}
func (a *App) form(w http.ResponseWriter, r *http.Request, v view, s Spec, id string, submitted bool) {
	ctx := r.Context()
	var e error
	v.Page = "form"
	v.Record = Row{}
	v.Options = map[string][][2]string{}
	for _, f := range s.Fields {
		v.Options[f.Kind] = enumOptions(f.Kind)
	}
	if id != "" {
		if s.Key == "energies" || s.Key == "commercial" {
			latest, err := query(ctx, a.db, "SELECT id FROM "+ident(s.Table)+" ORDER BY id DESC LIMIT 1")
			if err != nil {
				a.fail(w, r, err)
				return
			}
			n, err := ParseID(id)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			if len(latest) == 0 {
				a.fail(w, r, sql.ErrNoRows)
				return
			}
			if str(latest[0]["id"]) != str(n) {
				a.fail(w, r, ErrHistoricalReading)
				return
			}
		}
		v.Record, e = one(ctx, a.db, s.Table, id)
		if e != nil {
			a.fail(w, r, e)
			return
		}
	}
	if s.Key == "titles" && id == "" && !submitted {
		v.Record, e = energyTitles(ctx, a.db, false)
		if e != nil {
			a.fail(w, r, e)
			return
		}
	}

	if s.Key == "tenants" || s.Key == "landlords" {
		owner := "tenant_id"
		if s.Key == "landlords" {
			owner = "landlord_id"
		}
		if id != "" {
			v.Contacts, e = query(ctx, a.db, "SELECT * FROM tb_contacts WHERE "+owner+"=? ORDER BY id", id)
			if e != nil {
				a.fail(w, r, e)
				return
			}
		}
		if len(v.Contacts) == 0 {
			v.Contacts = []Row{{"contact_type": "CELULAR"}}
		}
	}
	if submitted {
		v.Record = Row{"id": id}
		for _, f := range s.Fields {
			v.Record[f.Column] = r.Form.Get(f.Name)
		}
		v.Contacts = nil
		for i := 0; i < 1000; i++ {
			b := fmt.Sprintf("contacts[%d].", i)
			if r.Form.Has(b + "contact") {
				v.Contacts = append(v.Contacts, Row{"contact": r.Form.Get(b + "contact"), "contact_type": r.Form.Get(b + "type")})
			}
		}
	}
	if s.Key == "properties" {
		v.Tenants, e = query(ctx, a.db, "SELECT id,name FROM tb_tenants ORDER BY name")
		if e != nil {
			a.fail(w, r, e)
			return
		}
	}
	v.Action = "/" + s.Key + "/save"
	if s.Key == "users" {
		v.Action = "/users"
	}
	a.render(w, v)
}
func (a *App) list(w http.ResponseWriter, r *http.Request, v view, s Spec) {
	ctx := r.Context()
	v.Page = "list"
	v.SortField = r.URL.Query().Get("sortField")
	order := "id"
	limit := ""
	if s.Key == "tenants" || s.Key == "landlords" {
		order = "name"
	}
	if s.Key == "users" {
		order = "username"
	}
	q := "SELECT * FROM " + ident(s.Table)
	if s.Key == "properties" {
		order = map[string]string{"tenant.name": "IF(t.name IS NULL,1,0),t.name", "propertyType": "CASE p.property_type WHEN 'APARTAMENTO' THEN 'apartamento' WHEN 'CONDOMINIO' THEN 'condomínio' WHEN 'LOJA' THEN 'loja' WHEN 'QUITINETE' THEN 'quitinete' WHEN 'SALA_COMERCIAL' THEN 'sala comercial' ELSE 'zzz' END", "number": "IF(p.number IS NULL,1,0),p.number", "paymentDay": "IF(p.payment_day IS NULL,1,0),CAST(p.payment_day AS SIGNED)", "value": "IF(p.rental_value IS NULL,1,0),p.rental_value"}[r.URL.Query().Get("sortField")]
		if order == "" {
			order = "IF(t.name IS NULL,1,0),t.name"
		}
		q = "SELECT p.*,t.name AS tenant_name FROM tb_properties p LEFT JOIN tb_tenants t ON t.id=p.tenant_id"
	}
	if s.Key == "energies" {
		order = "id DESC"
		limit = " LIMIT 3"
		if r.URL.Query().Get("history") == "1" {
			limit = ""
		}
	}
	var e error
	v.Rows, e = query(ctx, a.db, q+" ORDER BY "+order+limit)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if s.Key == "properties" {
		v.Landlords, e = query(ctx, a.db, "SELECT * FROM tb_landlords ORDER BY name")
		if e != nil {
			a.fail(w, r, e)
			return
		}
	}
	if s.Key == "tenants" || s.Key == "landlords" {
		owner := "tenant_id"
		if s.Key == "landlords" {
			owner = "landlord_id"
		}
		contacts, e := query(ctx, a.db, "SELECT * FROM tb_contacts")
		if e != nil {
			a.fail(w, r, e)
			return
		}
		for _, row := range v.Rows {
			cs := []string{}
			contactList := []Row{}
			for _, c := range contacts {
				if str(c[owner]) == str(row["id"]) {
					cs = append(cs, str(c["contact"]))
					contactList = append(contactList, c)
				}
			}
			row["contacts"] = strings.Join(cs, " / ")
			row["contact_list"] = contactList
		}
	}
	if s.Key == "tenants" {
		properties, err := query(ctx, a.db, "SELECT id,tenant_id FROM tb_properties WHERE tenant_id IS NOT NULL")
		if err != nil {
			a.fail(w, r, err)
			return
		}
		for _, row := range v.Rows {
			linked := []Row{}
			for _, p := range properties {
				if str(p["tenant_id"]) == str(row["id"]) {
					linked = append(linked, p)
				}
			}
			row["properties"] = linked
		}
	}

	if s.Key == "energies" {
		commercialLimit := " LIMIT 2"
		if r.URL.Query().Get("history") == "1" {
			commercialLimit = ""
		}
		v.Commercial, e = query(ctx, a.db, "SELECT * FROM tb_commercial_energy ORDER BY id DESC"+commercialLimit)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		v.Record, e = energyTitles(ctx, a.db, false)
		if e != nil {
			a.fail(w, r, e)
			return
		}

		slices.Reverse(v.Rows)
		slices.Reverse(v.Commercial)
		v.Page = "energy"
	}
	a.render(w, v)
}
func (a *App) password(w http.ResponseWriter, r *http.Request, v view) {
	v.Page = "password"
	if r.Method == "POST" {
		p := r.Form.Get("password")
		if strings.TrimSpace(p) == "" || p != r.Form.Get("confirmPassword") || len(p) > 72 {
			v.Error = "Preencha senhas iguais de até 72 bytes."
		} else {
			h, e := bcrypt.GenerateFromPassword([]byte(p), 10)
			if e == nil {
				_, e = a.db.ExecContext(r.Context(), "UPDATE tb_users SET password=?,updated_at=? WHERE id=?", string(h), a.now(), v.User["id"])
			}
			if e != nil {
				a.fail(w, r, e)
				return
			}
			http.Redirect(w, r, "/", 303)
			return
		}
	}
	a.render(w, v)
}
func ParseID(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n <= 0 {
		return 0, fmt.Errorf("ID inválido")
	}
	return n, nil
}
func OpenDatabase(ctx context.Context, c Config) (*sql.DB, error) {
	dsn, e := c.Database.DSN()
	if e != nil {
		return nil, e
	}
	db, e := sql.Open("mysql", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(c.Database.MaxOpen)
	db.SetMaxIdleConns(c.Database.MaxIdle)
	db.SetConnMaxLifetime(5 * time.Minute)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}

// ValidateSchema reads metadata only. It never creates, migrates or alters tables.
func ValidateSchema(ctx context.Context, db *sql.DB) error {
	for _, s := range specs {
		cols := []string{"id"}
		for _, f := range s.Fields {
			cols = append(cols, ident(f.Column))
		}
		if s.Timestamps {
			cols = append(cols, "created_at", "updated_at")
		}
		rows, e := db.QueryContext(ctx, "SELECT "+strings.Join(cols, ",")+" FROM "+ident(s.Table)+" LIMIT 0")
		if e != nil {
			return fmt.Errorf("schema %s: %w", s.Table, e)
		}
		rows.Close()
	}
	for table, cols := range map[string]string{"tb_contacts": "id,contact_type,contact,tenant_id,landlord_id,created_at,updated_at", "tb_properties_logs": "id,property_id,property_type,number,address,rental_value,complement,observation,payment_day,tenant_name,tenant_cpf_cnpj,created_at"} {
		rows, e := db.QueryContext(ctx, "SELECT "+cols+" FROM "+table+" LIMIT 0")
		if e != nil {
			return e
		}
		rows.Close()
	}
	return nil
}

func aLocation(c Config) *time.Location {
	loc, e := time.LoadLocation(c.Database.Timezone)
	if e != nil {
		return time.UTC
	}
	return loc
}
