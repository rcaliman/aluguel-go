package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Both the table and its edit form use the same current titles and defaults.
func energyTitles(ctx context.Context, db queryer, lock bool) (Row, error) {
	statement := "SELECT * FROM tb_energy_title ORDER BY id DESC LIMIT 1"
	if lock {
		statement += " FOR UPDATE"
	}
	rows, e := query(ctx, db, statement)
	if e != nil {
		return nil, e
	}
	titles := Row{}
	if len(rows) > 0 {
		titles = cloneRow(rows[0])
	}
	for i := 1; i <= 5; i++ {
		key := fmt.Sprintf("title_amount_%d", i)
		if strings.TrimSpace(str(titles[key])) == "" {
			number := i
			if i > 3 {
				number -= 3
			}
			titles[key] = fmt.Sprintf("conta %d", number)
		}
	}
	return titles, nil
}

func (a *App) saveEnergyTitles(ctx context.Context, form url.Values) error {
	tx, e := a.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	current, e := energyTitles(ctx, tx, true)
	if e != nil {
		return e
	}
	if want := form.Get("_version"); want != "" && recordVersion(current, nil) != want {
		return ErrConflict
	}
	values := Row{}
	for _, field := range specs["titles"].Fields {
		values[field.Column] = current[field.Column]
		if form.Has(field.Name) {
			value := strings.TrimSpace(form.Get(field.Name))
			if len([]rune(value)) > 255 {
				return fmt.Errorf("%s deve ter até 255 caracteres", field.Label)
			}
			if value == "" {
				values[field.Column] = nil
			} else {
				values[field.Column] = value
			}
		}
	}
	// Keep the existing append-only title snapshots; never overwrite an older row.
	if _, e = insert(ctx, tx, "tb_energy_title", values); e != nil {
		return e
	}
	return tx.Commit()
}
