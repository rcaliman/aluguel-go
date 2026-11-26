package app

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

var months = []string{"Janeiro", "Fevereiro", "Março", "Abril", "Maio", "Junho", "Julho", "Agosto", "Setembro", "Outubro", "Novembro", "Dezembro"}

func money(v any) string {
	if v == nil {
		return "—"
	}
	parts := strings.Split(fmt.Sprintf("%.2f", num(v)), ".")
	whole := parts[0]
	sign := ""
	if strings.HasPrefix(whole, "-") {
		sign = "-"
		whole = strings.TrimPrefix(whole, "-")
	}
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "." + whole[i:]
	}
	if len(parts) != 2 {
		return "valor inválido"
	}
	return "R$ " + sign + whole + "," + parts[1]
}
func words(v any) string {
	amount := num(v)
	if math.IsNaN(amount) || math.IsInf(amount, 0) || math.Abs(amount) > 9e15 {
		return "valor fora do intervalo suportado"
	}
	// Round the original floating-point value directly to two decimal places.
	// Multiplication by 100 first can introduce a second rounding (2.675 -> 2.68).
	rounded := strconv.FormatFloat(amount, 'f', 2, 64)
	n, _ := strconv.ParseInt(strings.Replace(rounded, ".", "", 1), 10, 64)
	if n == 0 {
		return "zero reais"
	}
	if n < 0 {
		return "menos " + words(float64(-n)/100)
	}
	r, c := n/100, n%100
	s := ""
	if r > 0 {
		s = partWords(r)
		if r == 1 {
			s += " real"
		} else {
			s += " reais"
		}
	}
	if c > 0 {
		if s != "" {
			s += " e "
		}
		s += partWords(c)
		if c == 1 {
			s += " centavo"
		} else {
			s += " centavos"
		}
	}
	return s
}
func partWords(n int64) string {
	u := []string{"", "um", "dois", "três", "quatro", "cinco", "seis", "sete", "oito", "nove"}
	special := []string{"dez", "onze", "doze", "treze", "catorze", "quinze", "dezesseis", "dezessete", "dezoito", "dezenove"}
	t := []string{"", "", "vinte", "trinta", "quarenta", "cinquenta", "sessenta", "setenta", "oitenta", "noventa"}
	h := []string{"", "cento", "duzentos", "trezentos", "quatrocentos", "quinhentos", "seiscentos", "setecentos", "oitocentos", "novecentos"}
	if n == 100 {
		return "cem"
	}
	if n >= 1000 {
		a, b := n/1000, n%1000
		s := "mil"
		if a != 1 {
			s = partWords(a) + " mil"
		}
		if b > 0 {
			if b <= 100 || b%100 == 0 {
				s += " e "
			} else {
				s += " "
			}
			s += partWords(b)
		}
		return s
	}
	if n >= 100 {
		s := h[n/100]
		if n%100 > 0 {
			s += " e " + partWords(n%100)
		}
		return s
	}
	if n >= 20 {
		s := t[n/10]
		if n%10 > 0 {
			s += " e " + partWords(n%10)
		}
		return s
	}
	if n >= 10 {
		return special[n-10]
	}
	return u[n]
}
func monthNumber(raw string) int {
	for i, m := range months {
		if strings.EqualFold(raw, m) {
			return i + 1
		}
	}
	n, _ := strconv.Atoi(raw)
	if n < 1 || n > 12 {
		return 0
	}
	return n
}
func document(v any) string {
	raw := str(v)
	s := ""
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			s += string(r)
		}
	}
	if len(s) == 11 {
		return s[:3] + "." + s[3:6] + "." + s[6:9] + "-" + s[9:]
	}
	if len(s) == 14 {
		return s[:2] + "." + s[2:5] + "." + s[5:8] + "/" + s[8:12] + "-" + s[12:]
	}
	return raw
}
func (a *App) receipts(w http.ResponseWriter, r *http.Request, v view) {
	if r.Method != "POST" || r.URL.Path != "/receipts/generate" {
		http.NotFound(w, r)
		return
	}
	ids := r.Form["propertyIds"]
	m := monthNumber(r.Form.Get("month"))
	year, e := strconv.Atoi(r.Form.Get("year"))
	if len(ids) == 0 || len(ids) > 500 || m == 0 || e != nil || year < 1900 || year > 9998 {
		http.Error(w, "Selecione imóveis, mês, ano e locador válidos.", 400)
		return
	}
	ctx := r.Context()
	landlord, e := one(ctx, a.db, "tb_landlords", r.Form.Get("landlordId"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	// Fetch the selected properties, tenants and first contact in one round trip.
	uniqueIDs := []string{}
	seen := map[string]bool{}
	for _, raw := range ids {
		n, err := ParseID(raw)
		if err != nil {
			http.Error(w, "Imóvel inválido.", 400)
			return
		}
		id := str(n)
		if !seen[id] {
			uniqueIDs = append(uniqueIDs, id)
			seen[id] = true
		}
	}
	marks := make([]string, len(uniqueIDs))
	args := make([]any, len(uniqueIDs))
	for i, id := range uniqueIDs {
		marks[i] = "?"
		args[i] = id
	}
	properties, e := query(ctx, a.db, "SELECT p.*,t.name AS receipt_tenant_name,t.cpf_cnpj AS receipt_tenant_document,(SELECT c.contact FROM tb_contacts c WHERE c.tenant_id=t.id ORDER BY c.id LIMIT 1) AS receipt_contact FROM tb_properties p LEFT JOIN tb_tenants t ON t.id=p.tenant_id WHERE p.id IN ("+strings.Join(marks, ",")+") ORDER BY p.id", args...)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if len(properties) != len(uniqueIDs) {
		http.Error(w, "Um dos imóveis selecionados não existe mais. Atualize a lista.", 404)
		return
	}
	v.Page = "receipts"
	v.Title = "Recibos"
	for _, p := range properties {
		if p["tenant_id"] == nil || p["receipt_tenant_name"] == nil {
			http.Error(w, "Não é possível emitir recibo de imóvel vago.", 400)
			return
		}
		t := Row{"name": p["receipt_tenant_name"], "cpf_cnpj": p["receipt_tenant_document"]}
		contact := str(p["receipt_contact"])
		receiptMonth, receiptYear := m, year
		if p["property_type"] == "CONDOMINIO" {
			receiptMonth++
			if receiptMonth > 12 {
				receiptMonth = 1
				receiptYear++
			}
		}
		v.Rows = append(v.Rows, Row{"locale": str(landlord["city"]) + " " + str(landlord["state"]), "day": p["payment_day"], "next_month": months[m%12], "next_year": year + 1, "tenant": t["name"], "tenant_document": document(t["cpf_cnpj"]), "value": words(p["rental_value"]), "property_type": p["property_type"], "number": p["number"], "observation": p["observation"], "month": months[m-1], "year": year, "date": fmt.Sprintf("%s %s, %s de %s de %d.", str(landlord["city"]), str(landlord["state"]), str(p["payment_day"]), months[receiptMonth-1], receiptYear), "landlord": landlord["name"], "landlord_document": document(landlord["cpf_cnpj"]), "contact": contact})
	}
	sort.SliceStable(v.Rows, func(i, j int) bool { return str(v.Rows[i]["tenant"]) < str(v.Rows[j]["tenant"]) })
	a.render(w, v)
}
func validDate(y, m int, day string) (time.Time, error) {
	d, e := strconv.Atoi(day)
	if e != nil || y < 1900 || y > 9998 || m < 1 || m > 12 {
		return time.Time{}, fmt.Errorf("data inválida")
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return t, fmt.Errorf("dia de pagamento não existe no mês informado")
	}
	return t, nil
}
func (a *App) contract(w http.ResponseWriter, r *http.Request, v view) {
	ctx := r.Context()
	if strings.HasPrefix(r.URL.Path, "/contracts/form/") && r.Method == "GET" {
		id := strings.TrimPrefix(r.URL.Path, "/contracts/form/")
		p, e := one(ctx, a.db, "tb_properties", id)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		if p["tenant_id"] == nil {
			http.Redirect(w, r, "/properties?error=ImovelVago", 303)
			return
		}
		v.Record = p
		v.Extra, e = one(ctx, a.db, "tb_tenants", p["tenant_id"])
		if e != nil {
			a.fail(w, r, e)
			return
		}
		v.Page = "contractform"
		v.Title = "Gerar contrato"
		v.Landlords, e = query(ctx, a.db, "SELECT * FROM tb_landlords ORDER BY name")
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.render(w, v)
		return
	}
	if r.URL.Path != "/contracts/editor" || r.Method != "POST" {
		http.NotFound(w, r)
		return
	}
	p, e := one(ctx, a.db, "tb_properties", r.Form.Get("propertyId"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if p["tenant_id"] == nil {
		http.Error(w, "Imóvel vago", 400)
		return
	}
	t, e := one(ctx, a.db, "tb_tenants", p["tenant_id"])
	if e != nil {
		a.fail(w, r, e)
		return
	}
	l, e := one(ctx, a.db, "tb_landlords", r.Form.Get("landlordId"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	sy, _ := strconv.Atoi(r.Form.Get("startYear"))
	ey, _ := strconv.Atoi(r.Form.Get("endYear"))
	sm := monthNumber(r.Form.Get("startMonth"))
	em := monthNumber(r.Form.Get("endMonth"))
	start, e := validDate(sy, sm, str(p["payment_day"]))
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	end, e := validDate(ey, em, str(p["payment_day"]))
	if e != nil || !end.After(start) {
		http.Error(w, "Informe um término posterior ao início e um dia válido.", 400)
		return
	}
	v.Page = "contract"
	v.Title = "Editor de contrato"
	v.Record = p
	v.Rows = []Row{t, l}
	v.Extra = Row{"tenant": t, "landlord": l, "property": p, "start": start.Format("02/01/2006"), "stop": end.Format("02/01/2006"), "months": (ey-sy)*12 + em - sm, "words": words(p["rental_value"]), "date": fmt.Sprintf("%s-%s, %s de %s de %d", str(l["city"]), str(l["state"]), str(p["payment_day"]), strings.ToLower(months[sm-1]), sy)}
	a.render(w, v)
}
func (a *App) info(w http.ResponseWriter, r *http.Request, v view, id string) {
	if r.Method != "GET" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	p, e := one(ctx, a.db, "tb_properties", id)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	v.Page = "info"
	v.Title = "Informações do imóvel"
	v.Record = p
	if p["tenant_id"] != nil {
		v.Extra, e = one(ctx, a.db, "tb_tenants", p["tenant_id"])
		if e != nil {
			a.fail(w, r, e)
			return
		}
		v.Contacts, e = query(ctx, a.db, "SELECT * FROM tb_contacts WHERE tenant_id=? ORDER BY id", p["tenant_id"])
		if e != nil {
			a.fail(w, r, e)
			return
		}
	}
	v.Logs, e = query(ctx, a.db, "SELECT * FROM tb_properties_logs WHERE property_id=? ORDER BY id DESC", id)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	if l := mostRecentRelevantLog(v.Logs); l != nil && l["rental_value"] != nil {
		if d, ok := l["created_at"].(time.Time); ok {
			d = d.In(aLocation(a.cfg))
			ipca, e := a.ipca(ctx, d, a.now())
			if e != nil {
				v.Correction = "Correção indisponível: não foi possível consultar o Bacen."
			} else {
				v.CorrectionData = Row{"originalValue": l["rental_value"], "originalDate": d, "correctedValue": num(l["rental_value"]) * (1 + ipca/100), "accumulatedIpcaPercentage": ipca}
				v.Correction = fmt.Sprintf("Valor original: %s | Data: %s | IPCA acumulado: %.2f%% | Valor corrigido: %s", money(l["rental_value"]), d.Format("02/01/2006"), ipca, money(num(l["rental_value"])*(1+ipca/100)))
			}
		}
	}

	a.render(w, v)
}

// Derive the reference from successive snapshots, including existing records.
// Address, notes and payment-day changes retain the original rental reference.
func mostRecentRelevantLog(logs []Row) Row {
	ordered := append([]Row(nil), logs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, _ := ordered[i]["created_at"].(time.Time)
		b, _ := ordered[j]["created_at"].(time.Time)
		if a.Equal(b) {
			ai, _ := strconv.ParseInt(str(ordered[i]["id"]), 10, 64)
			bi, _ := strconv.ParseInt(str(ordered[j]["id"]), 10, 64)
			return ai < bi
		}
		return a.Before(b)
	})
	var previous, reference Row
	for _, r := range ordered {
		if _, ok := r["created_at"].(time.Time); !ok {
			continue
		}
		if previous == nil || !sameRent(previous["rental_value"], r["rental_value"]) || !sameTenant(previous, r) {
			reference = r
		}
		previous = r
	}
	if reference == nil || reference["rental_value"] == nil {
		return nil
	}
	return reference
}
func sameRent(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return num(a) == num(b)
}
func tenantName(r Row) string {
	return strings.ToLower(strings.Join(strings.Fields(str(r["tenant_name"])), " "))
}
func tenantDocument(r Row) string {
	raw := str(r["tenant_cpf_cnpj"])
	digits := ""
	for _, c := range raw {
		if c >= '0' && c <= '9' {
			digits += string(c)
		}
	}
	return digits
}
func sameTenant(a, b Row) bool {
	ad, bd := tenantDocument(a), tenantDocument(b)
	if ad != "" && bd != "" {
		return ad == bd
	}
	an, bn := tenantName(a), tenantName(b)
	if an != "" || bn != "" {
		return an == bn
	}
	return ad == bd
}
