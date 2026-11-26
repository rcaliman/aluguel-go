package app

import (
	"fmt"
	"html/template"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func uiTruthy(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Bool:
		return r.Bool()
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return r.Len() > 0
	case reflect.Int, reflect.Int64, reflect.Int32:
		return r.Int() != 0
	case reflect.Float32, reflect.Float64:
		return r.Float() != 0
	}
	return true
}
func uiEmpty(v any) bool { return !uiTruthy(v) }
func uiLen(v any) int {
	if v == nil {
		return 0
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return r.Len()
	}
	return 0
}
func uiEqual(a, b any) bool {
	if uiNil(a) || uiNil(b) {
		return uiNil(a) && uiNil(b)
	}
	return str(a) == str(b)
}
func uiAdd(a, b any) any {
	if _, ok := a.(string); ok {
		return str(a) + str(b)
	}
	if _, ok := b.(string); ok {
		return str(a) + str(b)
	}
	return num(a) + num(b)
}
func uiFormValue(v any) any {
	switch r := v.(type) {
	case Row:
		return r["id"]
	case map[string]any:
		return r["id"]
	}
	return v
}
func uiSnake(s string) string {
	var b strings.Builder
	previous := rune(0)
	for i, c := range s {
		if unicode.IsDigit(c) && unicode.IsLetter(previous) {
			b.WriteByte('_')
		}
		if unicode.IsUpper(c) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
		} else {
			b.WriteRune(c)
		}
		previous = c
	}
	return b.String()
}
func uiField(v any, key string) any {
	if v == nil {
		return nil
	}
	if key == "empty" || key == "isEmpty" {
		return uiEmpty(v)
	}
	if key == "size" || key == "length" {
		return uiLen(v)
	}
	var row Row
	switch r := v.(type) {
	case Row:
		row = r
	case map[string]any:
		row = Row(r)
	case string:
		switch key {
		case "name":
			return r
		case "displayName":
			return display(r)
		}
		return nil
	default:
		return nil
	}
	if key == "contacts" {
		if cs, ok := row["contact_list"]; ok {
			return cs
		}
		switch cs := row["contacts"].(type) {
		case []Row:
			return cs
		case string:
			if cs != "" {
				return []Row{{"contact": cs, "contact_type": "CELULAR"}}
			}
		}
		return []Row{}
	}
	if val, ok := row[key]; ok {
		return val
	}
	if val, ok := row[uiSnake(key)]; ok {
		return val
	}
	aliases := map[string]string{"value": "rental_value", "type": "contact_type", "name": "username", "propertyNumber": "number", "landlordCpf": "landlord_document", "tenantContact": "contact", "tenantDocument": "tenant_document", "isLast": "last"}
	if alias, ok := aliases[key]; ok {
		return row[alias]
	}
	if key == "tenant" {
		if id := row["tenant_id"]; id != nil {
			return Row{"id": id, "name": row["tenant_name"]}
		}
	}
	return nil
}
func uiEnums(kind string) []string {
	out := []string{}
	for _, o := range enumOptions(kind) {
		out = append(out, o[0])
	}
	return out
}
func uiIteration(i int) Row {
	return Row{"index": i, "count": i + 1, "first": i == 0, "even": i%2 == 0, "odd": i%2 != 0}
}
func uiRowsLast(rows []Row) []Row {
	out := make([]Row, len(rows))
	for i, r := range rows {
		out[i] = cloneRow(r)
		out[i]["last"] = i == len(rows)-1
	}
	return out
}
func uiRecord(v view) Row {
	r := cloneRow(v.Record)
	if str(r["id"]) == "" {
		delete(r, "id")
	}
	if v.Page == "form" && (v.Key == "tenants" || v.Key == "landlords") {
		r["contacts"] = v.Contacts
	}
	return r
}
func uiModel(v view) Row {
	record := uiRecord(v)
	tenant, landlord := v.Extra, Row(nil)
	if v.Page == "contract" || v.Page == "contractform" {
		if t, ok := v.Extra["tenant"].(Row); ok {
			tenant = t
		}
		landlord, _ = v.Extra["landlord"].(Row)
	}
	if v.Page == "info" && tenant != nil {
		tenant = cloneRow(tenant)
		tenant["contacts"] = v.Contacts
	}
	if v.Key == "tenants" && v.Page == "form" {
		tenant = record
	}
	if v.Key == "landlords" && v.Page == "form" {
		landlord = record
	}
	user := record
	tenants, landlords := v.Tenants, v.Landlords
	if v.Page == "list" && v.Key == "tenants" {
		tenants = v.Rows
	}
	if v.Page == "list" && v.Key == "landlords" {
		landlords = v.Rows
	}
	years := []int{}
	for y := v.Year - 5; y <= v.Year+2; y++ {
		years = append(years, y)
	}
	errorMessage := any(v.Error)
	if v.Error == "" || v.Page == "login" {
		errorMessage = nil
	}
	var correction any
	if len(v.CorrectionData) > 0 {
		correction = v.CorrectionData
	}
	return Row{"property": record, "tenant": tenant, "landlord": landlord, "user": user, "energy": record, "commercialEnergy": record, "energyTitle": record, "energyTitles": record, "properties": v.Rows, "tenants": tenants, "landlords": landlords, "users": v.Rows, "energyReadings": uiEnergyRows(v.Rows, v.PageNumber), "commercialEnergyReadings": uiEnergyRows(v.Commercial, v.PageNumber), "receipts": v.Rows, "propertyLog": v.Logs, "rentalCorrection": correction, "propertyTypes": uiEnums("PropertyTypeEnum"), "propertyUseTypes": uiEnums("PropertyUseTypeEnum"), "contactTypeOptions": uiEnums("ContactTypeEnum"), "maritalStatusOptions": uiEnums("MaritalStatusEnum"), "roles": uiEnums("RoleEnum"), "years": years, "currentYear": v.Year, "currentMonth": v.Month, "currentPage": v.Page, "sortField": v.SortField, "id": v.User["id"], "param": Row{"error": v.Page == "login" && v.Error != "", "logout": false}, "errorMessage": errorMessage, "successMessage": nil}
}

func uiFunctions(c Config) template.FuncMap {
	loc := aLocation(c)
	return template.FuncMap{
		"uiMoney": func(v any) string { return strings.Replace(money(v), "R$ ", "R$\u00a0", 1) }, "uiModel": uiModel, "uiField": uiField, "uiIteration": uiIteration, "uiFormValue": uiFormValue, "uiEmpty": uiEmpty, "uiLen": uiLen, "uiEqual": uiEqual, "uiDifferent": func(a, b any) bool { return !uiEqual(a, b) }, "uiNot": func(v any) bool { return !uiTruthy(v) }, "uiAdd": uiAdd, "uiSub": func(a, b any) float64 { return num(a) - num(b) }, "uiMul": func(a, b any) float64 { return num(a) * num(b) }, "uiDiv": func(a, b any) any {
			if num(b) == 0 {
				return nil
			}
			return num(a) / num(b)
		}, "uiNeg": func(v any) float64 { return -num(v) }, "uiGreater": func(a, b any) bool { return num(a) > num(b) }, "uiLess": func(a, b any) bool { return num(a) < num(b) }, "uiAtLeast": func(a, b any) bool { return num(a) >= num(b) }, "uiAtMost": func(a, b any) bool { return num(a) <= num(b) }, "uiChoose": func(condition, a, b any) any {
			if uiTruthy(condition) {
				return a
			}
			return b
		}, "uiCoalesce": func(a, b any) any {
			if uiEmpty(a) {
				return b
			}
			return a
		}, "uiLower": func(v any) string { return strings.ToLower(display(v)) }, "uiUpper": func(v any) string { return strings.ToUpper(display(v)) }, "uiReplace": func(v, a, b any) string { return strings.ReplaceAll(str(v), str(a), str(b)) },
		"uiText": func(v any) string {
			if t, ok := v.(time.Time); ok {
				return str(t.In(loc))
			}
			return str(v)
		},
		"uiDecimal": func(v any, args ...any) string {
			if v == nil {
				return ""
			}
			precision := 2
			if len(args) > 1 {
				precision = int(num(args[1]))
			}
			s := strconv.FormatFloat(num(v), 'f', precision, 64)
			if len(args) < 3 || str(args[2]) != "POINT" {
				s = strings.ReplaceAll(s, ".", ",")
			}
			return s
		},
		"uiDate": func(v any, pattern string) string {
			if v == nil {
				return ""
			}
			var date time.Time
			switch x := v.(type) {
			case time.Time:
				date = x.In(loc)
			case string:
				var e error
				date, e = time.Parse("2006-01-02", x)
				if e != nil {
					return x
				}
			default:
				return ""
			}
			layout := strings.NewReplacer("yyyy", "2006", "dd", "02", "MM", "01", "HH", "15", "mm", "04").Replace(pattern)
			return date.Format(layout)
		},
		"uiAge": func(v any) Row {
			age := Row{"years": 0, "months": 0}
			if date, ok := v.(time.Time); ok {
				date = date.In(loc)
				now := time.Now().In(loc)
				months := (now.Year()-date.Year())*12 + int(now.Month()) - int(date.Month())
				if now.Day() < date.Day() {
					months--
				}
				if months > 0 {
					age["years"] = months / 12
					age["months"] = months % 12
				}
			}
			return age
		},
		"uiPercent": func(part, total any) string {
			if part == nil || num(total) == 0 {
				return ""
			}
			return fmt.Sprintf("%.2f%%", num(part)/num(total)*100)
		},
	}
}

func uiNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Map, reflect.Slice, reflect.Pointer, reflect.Interface:
		return r.IsNil()
	}
	return false
}

func uiEnergyRows(rows []Row, page int) []Row {
	result := uiRowsLast(rows)
	if page > 1 {
		for _, row := range result {
			row["last"] = false
		}
	}
	return result
}
