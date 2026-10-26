package app

import (
	"context"
	"database/sql"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func titleRows(values ...any) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "title_amount_1", "title_amount_2", "title_amount_3", "title_amount_4", "title_amount_5"}).AddRow(7, values[0], values[1], values[2], values[3], values[4])
}

func TestTitleFormDisplaysCurrentTitles(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(strconv.FormatBool(empty), func(t *testing.T) {
			a, m := testApp(t)
			a.sessions["operator"] = session{UserID: "1", CSRF: "token", Expires: time.Now().Add(time.Hour)}
			m.ExpectQuery("SELECT \\* FROM `tb_users` WHERE id = \\?").WithArgs("1").WillReturnRows(sqlmock.NewRows([]string{"id", "role"}).AddRow(1, "OPERADOR"))
			expected := []string{"Quitinete & 1", "Quitinete 2", "Quitinete 3", "Loja 1", "Loja 2"}
			data := titleRows(expected[0], expected[1], expected[2], expected[3], expected[4])
			if empty {
				data = sqlmock.NewRows([]string{"id"})
				expected = []string{"conta 1", "conta 2", "conta 3", "conta 1", "conta 2"}
			}
			m.ExpectQuery("SELECT \\* FROM tb_energy_title ORDER BY id DESC LIMIT 1$").WillReturnRows(data)
			req := httptest.NewRequest("GET", "/energies/titleform", nil)
			req.AddCookie(&http.Cookie{Name: "ALUGUEL_SESSION", Value: "operator"})
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			for i, want := range expected {
				tag := regexp.MustCompile(`<input\b[^>]*\bname="titleAmount` + strconv.Itoa(i+1) + `"[^>]*>`).FindString(w.Body.String())
				if !strings.Contains(html.UnescapeString(tag), `value="`+want+`"`) {
					t.Fatalf("title %d: wanted %q in %s", i+1, want, tag)
				}
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestChangingOneTitlePreservesOtherTitles(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(strconv.FormatBool(partial), func(t *testing.T) {
			a, m := testApp(t)
			m.ExpectBegin()
			m.ExpectQuery("SELECT \\* FROM tb_energy_title ORDER BY id DESC LIMIT 1 FOR UPDATE").WillReturnRows(titleRows("Kit 1", "Kit 2", "Kit 3", "Loja 1", "Loja 2"))
			m.ExpectExec("INSERT INTO `tb_energy_title` \\(`title_amount_1`,`title_amount_2`,`title_amount_3`,`title_amount_4`,`title_amount_5`\\)").WithArgs("Kit 1", "Novo título", "Kit 3", "Loja 1", "Loja 2").WillReturnResult(sqlmock.NewResult(8, 1))
			m.ExpectCommit()
			form := url.Values{"titleAmount2": {"Novo título"}}
			if !partial {
				form.Set("titleAmount1", "Kit 1")
				form.Set("titleAmount3", "Kit 3")
				form.Set("titleAmount4", "Loja 1")
				form.Set("titleAmount5", "Loja 2")
			}
			if e := a.save(context.Background(), specs["titles"], form); e != nil {
				t.Fatal(e)
			}
			if e := m.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestTitleDefaultsAndFailedSaveRollback(t *testing.T) {
	a, m := testApp(t)
	m.ExpectBegin()
	m.ExpectQuery("SELECT \\* FROM tb_energy_title ORDER BY id DESC LIMIT 1 FOR UPDATE").WillReturnRows(titleRows("Kit 1", nil, "  ", "Loja 1", "Loja 2"))
	m.ExpectExec("INSERT INTO `tb_energy_title`").WithArgs("Kit 1", "conta 2", "conta 3", "Loja nova", "Loja 2").WillReturnError(sql.ErrConnDone)
	m.ExpectRollback()
	if e := a.save(context.Background(), specs["titles"], url.Values{"titleAmount4": {"Loja nova"}}); e == nil {
		t.Fatal("failed save must not report success")
	}
	if e := m.ExpectationsWereMet(); e != nil {
		t.Fatal(e)
	}
}
