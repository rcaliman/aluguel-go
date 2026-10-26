package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

type energyChange struct {
	ID     string
	Values Row
}

func cloneRow(r Row) Row {
	out := Row{}
	for k, v := range r {
		out[k] = v
	}
	return out
}

func derivedEnergyFields(key string) []string {
	if key == "energies" {
		return []string{"amount_1", "amount_2", "amount_3"}
	}
	return []string{"amount_1", "amount_2", "calculated_consumption_1", "calculated_consumption_2"}
}

var ErrHistoricalReading = errors.New("Somente a última leitura de energia pode ser alterada.")

// Rows are locked and ordered by ID. Recheck the last ID at save time so a
// form opened before a new reading was added cannot modify historical data.
func energyPlan(key, id string, values Row, rows []Row) ([]energyChange, error) {
	current := cloneRow(values)
	for _, k := range []string{"counter_1", "counter_2", "counter_3", "bill_amount", "kwh_value", "internal_counter", "account_consumption", "account_value"} {
		if current[k] != nil && num(current[k]) < 0 {
			return nil, fmt.Errorf("leituras, consumos e valores da conta não podem ser negativos")
		}
	}
	index := len(rows)
	if id != "" {
		index = -1
		for i, r := range rows {
			if str(r["id"]) == id {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, sql.ErrNoRows
		}
		if index != len(rows)-1 {
			return nil, ErrHistoricalReading
		}
	}
	var previous Row
	if index > 0 {
		previous = rows[index-1]
	}
	if e := calculateEnergy(key, current, previous); e != nil {
		return nil, e
	}
	changes := []energyChange{{ID: id, Values: current}}

	for _, change := range changes {
		for _, k := range derivedEnergyFields(key) {
			if change.Values[k] != nil {
				n := num(change.Values[k])
				if n < 0 {
					return nil, fmt.Errorf("rateio inválido na leitura %s: consumo interno deve estar entre zero e o consumo da conta", change.ID)
				}
				if math.IsNaN(n) || math.IsInf(n, 0) {
					return nil, fmt.Errorf("rateio fora do intervalo suportado")
				}
			}
		}
	}
	return changes, nil
}

func saveEnergy(ctx context.Context, tx *sql.Tx, s Spec, id string, values Row) error {
	// Lock the sequence through validation and saving of the latest reading.
	rows, e := query(ctx, tx, "SELECT * FROM "+ident(s.Table)+" ORDER BY id ASC FOR UPDATE")
	if e != nil {
		return e
	}
	changes, e := energyPlan(s.Key, id, values, rows)
	if e != nil {
		return e
	}
	for _, change := range changes {
		if change.ID == "" {
			_, e = insert(ctx, tx, s.Table, change.Values)
		} else {
			e = update(ctx, tx, s.Table, change.ID, change.Values)
		}
		if e != nil {
			return e
		}
	}
	return nil
}

func allocationWarning(r Row) string {
	if num(r["bill_amount"]) > 0 && r["amount_1"] == nil && r["amount_2"] == nil && r["amount_3"] == nil {
		return "Rateio indisponível: falta leitura anterior ou consumo medido."
	}
	return ""
}
