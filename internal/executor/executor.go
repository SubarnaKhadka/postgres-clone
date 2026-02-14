package executor

import (
	"fmt"
	"log/slog"

	"gopostgres/internal/ast"
	"gopostgres/internal/catalog"
	"gopostgres/internal/storage"
	"gopostgres/internal/types"
)

type Result struct {
	Tag     string
	Columns []*catalog.ColumnDef
	Rows    [][]any
}

type Executor struct {
	catalog *catalog.Catalog
}

func NewExecutor(cat *catalog.Catalog) *Executor {
	return &Executor{catalog: cat}
}

func (e *Executor) Execute(stmt ast.Statement) (*Result, error) {
	switch s := stmt.(type) {
	case *ast.CreateTableStatement:
		return e.executeCreateTable(s)
	case *ast.DropTableStatement:
		return e.executeDropTable(s)
	case *ast.InsertStatement:
		return e.executeInsert(s)
	case *ast.SelectStatement:
		return e.executeSelect(s)
	}
	return nil, fmt.Errorf("unsupported statement type")
}

func (e *Executor) executeCreateTable(stmt *ast.CreateTableStatement) (*Result, error) {
	if len(stmt.Columns) == 0 {
		return nil, fmt.Errorf("table must have at least one column")
	}
	columns := make([]*catalog.ColumnDef, len(stmt.Columns))
	for i, col := range stmt.Columns {
		oid, ok := types.OIDByName(col.TypeName)
		if !ok {
			return nil, fmt.Errorf("type %q does not exist", col.TypeName)
		}
		columns[i] = &catalog.ColumnDef{
			Name:    col.Name,
			TypeOID: oid,
			TypeMod: -1,
			NotNull: col.NotNull,
		}
	}
	if err := e.catalog.CreateTable("public", stmt.Name, columns); err != nil {
		return nil, err
	}
	return &Result{Tag: "CREATE TABLE"}, nil
}

func (e *Executor) executeSelect(stmt *ast.SelectStatement) (*Result, error) {
	table, err := e.catalog.GetTable("public", stmt.Table)
	if err != nil {
		return nil, err
	}

	var columns []*catalog.ColumnDef
	var columnIndexes []int

	if len(stmt.Columns) == 1 && stmt.Columns[0] == "*" {
		columns = table.Columns
		for i := range table.Columns {
			columnIndexes = append(columnIndexes, i)
		}
	} else {
		for _, name := range stmt.Columns {
			found := false
			for i, col := range table.Columns {
				if col.Name == name {
					columns = append(columns, col)
					columnIndexes = append(columnIndexes, i)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("column %q does not exist", name)
			}
		}
	}

	heap, err := storage.NewHeapFile(table.OID)
	if err != nil {
		return nil, err
	}
	defer heap.Close()

	// sequential scan
	var rows [][]any
	pageCount, err := heap.PageCount()
	if err != nil {
		return nil, err
	}

	for pageNum := uint32(0); pageNum < pageCount; pageNum++ {
		page, err := heap.ReadPage(pageNum)
		if err != nil {
			return nil, err
		}

		for itemIdx := uint16(0); itemIdx < page.GetItemCount(); itemIdx++ {
			data, err := page.GetTuple(itemIdx)
			if err != nil {
				return nil, err
			}

			allValues, err := storage.DecodeTuple(data, table.Columns)
			if err != nil {
				return nil, err
			}

			// evaluate WHERE clause - skip row if false
			if stmt.Where != nil {
				match, err := evalExpr(stmt.Where, allValues, table.Columns)
				if err != nil {
					return nil, err
				}
				if match != true {
					continue
				}
			}

			// pick only requested columns
			row := make([]any, len(columnIndexes))
			for i, idx := range columnIndexes {
				row[i] = allValues[idx]
			}
			rows = append(rows, row)
		}
	}
	rowCount := len(rows)
	return &Result{
		Tag:     fmt.Sprintf("SELECT %d", rowCount),
		Columns: columns,
		Rows:    rows,
	}, nil
}

func (e *Executor) executeDropTable(stmt *ast.DropTableStatement) (*Result, error) {
	if err := e.catalog.DropTable("public", stmt.Name, stmt.IfExists); err != nil {
		return nil, err
	}
	return &Result{Tag: "DROP TABLE"}, nil
}

func (e *Executor) executeInsert(stmt *ast.InsertStatement) (*Result, error) {
	table, err := e.catalog.GetTable("public", stmt.Table)
	if err != nil {
		return nil, err
	}
	columns := table.Columns
	if len(stmt.Columns) > 0 {
		columns, err = resolveColumns(table, stmt.Columns)
		if err != nil {
			return nil, err
		}
	}
	if len(stmt.Values) != len(columns) {
		return nil, fmt.Errorf("expected %d values, got %d", len(columns),
			len(stmt.Values))
	}

	// convert expressions to Go values
	values := make([]any, len(columns))
	for i, expr := range stmt.Values {
		val, err := evalLiteral(expr, columns[i])
		if err != nil {
			return nil, err
		}
		values[i] = val
	}

	// check NOT NULL constraints
	for i, col := range columns {
		if col.NotNull && values[i] == nil {
			return nil, fmt.Errorf("null value in column %q violates not-null constraint", col.Name)
		}
	}

	data, err := storage.EncodeTuple(values, columns)
	if err != nil {
		return nil, err
	}

	heap, err := storage.NewHeapFile(table.OID)
	if err != nil {
		return nil, err
	}
	defer heap.Close()

	_, _, err = heap.InsertTuple(data)
	if err != nil {
		return nil, err
	}

	return &Result{Tag: "INSERT 0 1"}, nil
}

func resolveColumns(table *catalog.TableDef, names []string) ([]*catalog.ColumnDef, error) {
	columns := make([]*catalog.ColumnDef, len(names))
	for i, name := range names {
		found := false
		for _, col := range table.Columns {
			if col.Name == name {
				columns[i] = col
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("column %q does not exist in table %q", name, table.Name)
		}
	}
	return columns, nil
}

func evalExpr(expr ast.Expression, row []any, columns []*catalog.ColumnDef) (any, error) {
	slog.Info("Received params", "expr", expr, "row", row, "columns", columns)
	// Will continue from here
	return nil, nil
}

func evalLiteral(expr ast.Expression, col *catalog.ColumnDef) (any,
	error,
) {
	switch e := expr.(type) {
	case *ast.NullLiteral:
		return nil, nil

	case *ast.IntegerLiteral:
		switch col.TypeOID {
		case types.OidInt2:
			return int16(e.Value), nil
		case types.OidInt4:
			return int32(e.Value), nil
		case types.OidInt8:
			return e.Value, nil
		case types.OidFloat4:
			return float32(e.Value), nil
		case types.OidFloat8:
			return float64(e.Value), nil
		case types.OidOid:
			return uint32(e.Value), nil
		}
		return nil, fmt.Errorf("cannot use integer literal for column %q of type OID %d", col.Name, col.TypeOID)

	case *ast.FloatLiteral:
		switch col.TypeOID {
		case types.OidFloat4:
			return float32(e.Value), nil
		case types.OidFloat8:
			return e.Value, nil
		}
		return nil, fmt.Errorf("cannot use float literal for column %q of type OID %d", col.Name, col.TypeOID)

	case *ast.StringLiteral:
		switch col.TypeOID {
		case types.OidText, types.OidVarchar, types.OidChar:
			return e.Value, nil
		case types.OidBytea:
			return []byte(e.Value), nil
		}
		return nil, fmt.Errorf("cannot use string literal for column %q of type OID %d", col.Name, col.TypeOID)

	case *ast.BoolLiteral:
		if col.TypeOID == types.OidBool {
			return e.Value, nil
		}
		return nil, fmt.Errorf("cannot use boolean literal for column %q of type OID %d", col.Name, col.TypeOID)
	}

	return nil, fmt.Errorf("unsupported expression type")
}
