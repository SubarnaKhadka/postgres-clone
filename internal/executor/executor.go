package executor

import (
	"fmt"

	"gopostgres/internal/ast"
	"gopostgres/internal/catalog"
	"gopostgres/internal/storage"
	"gopostgres/internal/txn"
	"gopostgres/internal/types"
)

type Result struct {
	Tag     string
	Columns []*catalog.ColumnDef
	Rows    [][]any
}

type Executor struct {
	catalog    *catalog.Catalog
	txnManager *txn.TxnManager
	txnXID     uint64
	snapShot   *txn.Snapshot
}

func NewExecutor(cat *catalog.Catalog, tm *txn.TxnManager, txnID uint64, snap *txn.Snapshot) *Executor {
	return &Executor{catalog: cat, txnManager: tm, txnXID: txnID, snapShot: snap}
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
	case *ast.UpdateStatement:
		return e.executeUpdate(s)
	case *ast.DeleteStatement:
		return e.executeDelete(s)
	}
	return nil, fmt.Errorf("unsupported statement type")
}

func (e *Executor) getAutoCommitWithXID() (uint64, bool) {
	xid := e.txnXID
	autoCommit := xid == 0
	if autoCommit {
		xid = e.txnManager.NextXID()
	}
	return xid, autoCommit
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

			header, allValues, err := storage.DecodeTuple(data, table.Columns)
			if err != nil {
				return nil, err
			}

			if !e.txnManager.IsVisible(header.Xmin, header.Xmax, e.snapShot) {
				continue
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

func (e *Executor) executeUpdate(stmt *ast.UpdateStatement) (*Result, error) {
	table, err := e.catalog.GetTable("public", stmt.Table)
	if err != nil {
		return nil, err
	}

	heap, err := storage.NewHeapFile(table.OID)
	if err != nil {
		return nil, err
	}
	defer heap.Close()

	updateCount := 0
	xid, autoCommit := e.getAutoCommitWithXID()

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
			header, allValues, err := storage.DecodeTuple(data, table.Columns)
			if err != nil {
				return nil, err
			}

			if !e.txnManager.IsVisible(header.Xmin, header.Xmax, e.snapShot) {
				continue
			}

			// evaluate where clause -skip if false
			if stmt.Where != nil {
				match, err := evalExpr(stmt.Where, allValues, table.Columns)
				if err != nil {
					return nil, err
				}
				if match != true {
					continue
				}
			}

			header.Xmax = xid
			if err := heap.UpdateTupleHeader(pageNum, itemIdx, header); err != nil {
				return nil, err
			}

			newValues := make([]any, len(allValues))
			copy(newValues, allValues)

			for _, clause := range stmt.SetClauses {
				found := false
				for i, col := range table.Columns {
					if col.Name == clause.Column {
						val, err := evalLiteral(clause.Value, col)
						if err != nil {
							if autoCommit {
								e.txnManager.Abort(xid)
							}
							return nil, err
						}
						newValues[i] = val
						found = true
						break
					}
				}
				if !found {
					if autoCommit {
						e.txnManager.Abort(xid)
					}
					return nil, fmt.Errorf("column %q does not exist in table %q", clause.Column, stmt.Table)
				}
			}

			newHeader := &storage.TupleHeader{Xmin: xid, Xmax: 0}
			newData, err := storage.EncodeTuple(newHeader, newValues, table.Columns)
			if err != nil {
				if autoCommit {
					e.txnManager.Abort(xid)
				}
				return nil, err
			}
			if _, _, err := heap.InsertTuple(newData); err != nil {
				if autoCommit {
					e.txnManager.Abort(xid)
				}
				return nil, err
			}

			updateCount++
		}
	}
	if autoCommit {
		e.txnManager.Commit(xid)
	}
	return &Result{Tag: fmt.Sprintf("UPDATE %d", updateCount)}, nil
}

func (e *Executor) executeDelete(stmt *ast.DeleteStatement) (*Result, error) {
	table, err := e.catalog.GetTable("public", stmt.Table)
	if err != nil {
		return nil, err
	}

	heap, err := storage.NewHeapFile(table.OID)
	if err != nil {
		return nil, err
	}
	defer heap.Close()

	xid, autoCommit := e.getAutoCommitWithXID()
	deleteCount := 0

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
			header, allValues, err := storage.DecodeTuple(data, table.Columns)
			if err != nil {
				return nil, err
			}

			if !e.txnManager.IsVisible(header.Xmin, header.Xmax, e.snapShot) {
				continue
			}

			// evaluate where clause -skip if false
			if stmt.Where != nil {
				match, err := evalExpr(stmt.Where, allValues, table.Columns)
				if err != nil {
					return nil, err
				}
				if match != true {
					continue
				}
			}

			// mark as deleted: set max
			header.Xmax = xid
			if err := heap.UpdateTupleHeader(pageNum, itemIdx, header); err != nil {
				if autoCommit {
					e.txnManager.Abort(xid)
				}
				return nil, err
			}
			deleteCount++
		}
	}
	if autoCommit {
		e.txnManager.Commit(xid)
	}
	return &Result{Tag: fmt.Sprintf("DELETE %d", deleteCount)}, nil
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

func compareFloat(l, r float64, op string) (bool, error) {
	switch op {
	case "=":
		return l == r, nil
	case "!=", "<>":
		return l != r, nil
	case "<":
		return l < r, nil
	case ">":
		return l > r, nil
	case "<=":
		return l <= r, nil
	case ">=":
		return l >= r, nil
	default:
		return false, fmt.Errorf("unsupported operator %q for comparison", op)
	}
}

func compareString(l, r string, op string) (bool, error) {
	switch op {
	case "=":
		return l == r, nil
	case "!=", "<>":
		return l != r, nil
	case "<":
		return l < r, nil
	case ">":
		return l > r, nil
	case "<=":
		return l <= r, nil
	case ">=":
		return l >= r, nil
	default:
		return false, fmt.Errorf("unsupported operator %q for comparison", op)
	}
}

func compareBool(l, r bool, op string) (bool, error) {
	switch op {
	case "=":
		return l == r, nil
	case "!=", "<>":
		return l != r, nil
	default:
		return false, fmt.Errorf("unsupported operator %q for comparison", op)
	}
}

func compareValues(left, right any, op string) (bool, error) {
	if left == nil || right == nil {
		return false, nil
	}

	lf, lok := toFloat64(left)
	rf, rok := toFloat64(right)
	if lok && rok {
		return compareFloat(lf, rf, op)
	}

	ls, lok := left.(string)
	rs, rok := right.(string)
	if lok && rok {
		return compareString(ls, rs, op)
	}

	lb, lok := left.(bool)
	rb, rok := right.(bool)
	if lok && rok {
		return compareBool(lb, rb, op)
	}
	return false, fmt.Errorf("cannot compare %T and %T", left, right)
}

func evalExpr(expr ast.Expression, row []any, columns []*catalog.ColumnDef) (any, error) {
	switch e := expr.(type) {
	case *ast.IntegerLiteral:
		return e.Value, nil
	case *ast.FloatLiteral:
		return e.Value, nil
	case *ast.StringLiteral:
		return e.Value, nil
	case *ast.BoolLiteral:
		return e.Value, nil
	case *ast.NullLiteral:
		return nil, nil
	case *ast.ColumnRef:
		for i, col := range columns {
			if col.Name == e.Name {
				return row[i], nil
			}
		}
		return nil, fmt.Errorf("column %q not found", e.Name)

	case *ast.IsNullExpr:
		val, err := evalExpr(e.Expr, row, columns)
		if err != nil {
			return nil, err
		}
		if e.Not {
			return val != nil, nil
		}
		return val == nil, nil

	case *ast.BinaryExpr:
		switch e.Op {
		case "AND":
			left, err := evalExpr(e.Left, row, columns)
			if err != nil {
				return nil, err
			}
			if left != true {
				return false, nil
			}
			return evalExpr(e.Right, row, columns)

		case "OR":
			left, err := evalExpr(e.Left, row, columns)
			if err != nil {
				return nil, err
			}
			if left == true {
				return true, nil
			}
			return evalExpr(e.Right, row, columns)

		default:
			left, err := evalExpr(e.Left, row, columns)
			if err != nil {
				return nil, err
			}
			right, err := evalExpr(e.Right, row, columns)
			if err != nil {
				return nil, err
			}
			return compareValues(left, right, e.Op)
		}
	}
	return nil, fmt.Errorf("unsupported expression type %T", expr)
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
	xid, autoCommit := e.getAutoCommitWithXID()
	header := &storage.TupleHeader{Xmin: xid, Xmax: 0}
	data, err := storage.EncodeTuple(header, values, columns)
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
		if autoCommit {
			e.txnManager.Abort(xid)
		}
		return nil, err
	}
	if autoCommit {
		e.txnManager.Commit(xid)
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
