package accdb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Query represents a database query
type Query struct {
	db     *Database
	table  *Table
	fields []string
	where  []Condition
	order  []OrderBy
	limit  int
	offset int
}

// Condition represents a WHERE condition
type Condition struct {
	Column   string
	Operator string
	Value    interface{}
	Logic    string // AND, OR
}

// OrderBy represents an ORDER BY clause
type OrderBy struct {
	Column string
	Desc   bool
}

// QueryResult holds query results
type QueryResult struct {
	Columns []string
	Rows    []map[string]interface{}
	Count   int
}

// Select creates a new SELECT query
func (db *Database) Select(tableName string, fields ...string) *Query {
	table, _ := db.Table(tableName)

	q := &Query{
		db:     db,
		table:  table,
		fields: fields,
		limit:  -1,
		offset: 0,
	}

	if len(fields) == 0 && table != nil {
		q.fields = table.ColumnNames()
	}

	return q
}

// Where adds a WHERE condition
func (q *Query) Where(column string, operator string, value interface{}) *Query {
	q.where = append(q.where, Condition{
		Column:   column,
		Operator: operator,
		Value:    value,
		Logic:    "AND",
	})
	return q
}

// OrWhere adds an OR WHERE condition
func (q *Query) OrWhere(column string, operator string, value interface{}) *Query {
	q.where = append(q.where, Condition{
		Column:   column,
		Operator: operator,
		Value:    value,
		Logic:    "OR",
	})
	return q
}

// OrderByAsc adds ascending order
func (q *Query) OrderByAsc(column string) *Query {
	q.order = append(q.order, OrderBy{Column: column, Desc: false})
	return q
}

// OrderByDesc adds descending order
func (q *Query) OrderByDesc(column string) *Query {
	q.order = append(q.order, OrderBy{Column: column, Desc: true})
	return q
}

// OrderBy adds an order clause with explicit direction
func (q *Query) OrderBy(column string, desc bool) *Query {
	q.order = append(q.order, OrderBy{Column: column, Desc: desc})
	return q
}

// Limit sets the result limit
func (q *Query) Limit(n int) *Query {
	q.limit = n
	return q
}

// Offset sets the result offset
func (q *Query) Offset(n int) *Query {
	q.offset = n
	return q
}

// Execute runs the query and returns results
func (q *Query) Execute() (*QueryResult, error) {
	if q.table == nil {
		return nil, ErrTableNotFound
	}

	result := &QueryResult{
		Columns: q.fields,
		Rows:    make([]map[string]interface{}, 0),
	}

	iter, err := q.table.Rows()
	if err != nil {
		return nil, err
	}

	count := 0
	skipped := 0

	for iter.Next() {
		row := iter.Row()

		// Apply WHERE conditions
		if !q.matchesConditions(row) {
			continue
		}

		// Apply OFFSET
		if skipped < q.offset {
			skipped++
			continue
		}

		// Apply LIMIT
		if q.limit >= 0 && count >= q.limit {
			break
		}

		// Select fields
		rowData := make(map[string]interface{})
		for _, field := range q.fields {
			if field == "*" {
				for k, v := range row.Values {
					rowData[k] = v
				}
			} else {
				rowData[field] = row.Values[field]
			}
		}

		result.Rows = append(result.Rows, rowData)
		count++
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	// Apply ORDER BY (in memory)
	if len(q.order) > 0 {
		q.sortResults(result)
	}

	result.Count = len(result.Rows)
	return result, nil
}

// matchesConditions checks if a row matches WHERE conditions
func (q *Query) matchesConditions(row *Row) bool {
	if len(q.where) == 0 {
		return true
	}

	result := true

	for i, cond := range q.where {
		matches := q.evaluateCondition(row, cond)

		if i == 0 {
			result = matches
		} else if cond.Logic == "OR" {
			result = result || matches
		} else {
			result = result && matches
		}
	}

	return result
}

// evaluateCondition evaluates a single condition
func (q *Query) evaluateCondition(row *Row, cond Condition) bool {
	value := row.Values[cond.Column]

	switch cond.Operator {
	case "=", "==":
		return compareEqual(value, cond.Value)
	case "!=", "<>":
		return !compareEqual(value, cond.Value)
	case "<":
		return compareLess(value, cond.Value)
	case "<=":
		return compareLess(value, cond.Value) || compareEqual(value, cond.Value)
	case ">":
		return compareGreater(value, cond.Value)
	case ">=":
		return compareGreater(value, cond.Value) || compareEqual(value, cond.Value)
	case "LIKE", "like":
		return compareLike(value, cond.Value)
	case "IN", "in":
		return compareIn(value, cond.Value)
	case "IS NULL", "is null":
		return value == nil
	case "IS NOT NULL", "is not null":
		return value != nil
	default:
		return false
	}
}

// compareEqual compares two values for equality
func compareEqual(a, b interface{}) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// compareLess compares if a < b
func compareLess(a, b interface{}) bool {
	af := toFloat(a)
	bf := toFloat(b)
	return af < bf
}

// compareGreater compares if a > b
func compareGreater(a, b interface{}) bool {
	af := toFloat(a)
	bf := toFloat(b)
	return af > bf
}

// compareLike performs LIKE comparison
func compareLike(a, b interface{}) bool {
	if a == nil || b == nil {
		return false
	}

	pattern := fmt.Sprint(b)
	value := fmt.Sprint(a)

	// Convert SQL LIKE pattern to regex
	pattern = regexp.QuoteMeta(pattern)
	pattern = strings.ReplaceAll(pattern, "%", ".*")
	pattern = strings.ReplaceAll(pattern, "_", ".")
	pattern = "^" + pattern + "$"

	matched, _ := regexp.MatchString("(?i)"+pattern, value)
	return matched
}

// compareIn checks if value is in a list
func compareIn(a, b interface{}) bool {
	if a == nil {
		return false
	}

	switch list := b.(type) {
	case []interface{}:
		for _, item := range list {
			if compareEqual(a, item) {
				return true
			}
		}
	case []string:
		as := fmt.Sprint(a)
		for _, item := range list {
			if as == item {
				return true
			}
		}
	case []int:
		ai := toInt(a)
		for _, item := range list {
			if ai == item {
				return true
			}
		}
	}

	return false
}

// toFloat converts a value to float64
func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	default:
		return 0
	}
}

// toInt converts a value to int
func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	default:
		return 0
	}
}

// sortResults sorts query results by ORDER BY clauses
func (q *Query) sortResults(result *QueryResult) {
	// Simple bubble sort for demonstration
	for i := 0; i < len(result.Rows)-1; i++ {
		for j := 0; j < len(result.Rows)-i-1; j++ {
			if q.shouldSwap(result.Rows[j], result.Rows[j+1]) {
				result.Rows[j], result.Rows[j+1] = result.Rows[j+1], result.Rows[j]
			}
		}
	}
}

// shouldSwap determines if two rows should be swapped
func (q *Query) shouldSwap(a, b map[string]interface{}) bool {
	for _, ord := range q.order {
		va := a[ord.Column]
		vb := b[ord.Column]

		if compareEqual(va, vb) {
			continue
		}

		less := compareLess(va, vb)
		if ord.Desc {
			return less
		}
		return !less
	}
	return false
}

// First returns the first result
func (q *Query) First() (map[string]interface{}, error) {
	q.limit = 1
	result, err := q.Execute()
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, nil
	}
	return result.Rows[0], nil
}

// SQL executes a raw SQL query
func (db *Database) SQL(query string) (*QueryResult, error) {
	query = strings.TrimSpace(query)
	upperQuery := strings.ToUpper(query)

	switch {
	case strings.HasPrefix(upperQuery, "SELECT"):
		return db.executeSelect(query)
	case strings.HasPrefix(upperQuery, "INSERT"):
		return nil, db.executeInsert(query)
	case strings.HasPrefix(upperQuery, "UPDATE"):
		return nil, db.executeUpdate(query)
	case strings.HasPrefix(upperQuery, "DELETE"):
		return nil, db.executeDelete(query)
	case strings.HasPrefix(upperQuery, "CREATE TABLE"):
		return nil, db.executeCreateTable(query)
	case strings.HasPrefix(upperQuery, "DROP TABLE"):
		return nil, db.executeDropTable(query)
	default:
		return nil, fmt.Errorf("unsupported SQL statement")
	}
}

// executeSelect parses and executes a SELECT statement
func (db *Database) executeSelect(sql string) (*QueryResult, error) {
	// Simple regex-based parser
	re := regexp.MustCompile(`(?i)SELECT\s+(.+?)\s+FROM\s+(\w+)(?:\s+WHERE\s+(.+?))?(?:\s+ORDER\s+BY\s+(.+?))?(?:\s+LIMIT\s+(\d+))?(?:\s+OFFSET\s+(\d+))?$`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 3 {
		return nil, fmt.Errorf("invalid SELECT syntax")
	}

	fields := parseFieldList(matches[1])
	tableName := matches[2]

	q := db.Select(tableName, fields...)

	// Parse WHERE clause
	if len(matches) > 3 && matches[3] != "" {
		conditions := parseWhereClause(matches[3])
		for _, cond := range conditions {
			q.where = append(q.where, cond)
		}
	}

	// Parse ORDER BY
	if len(matches) > 4 && matches[4] != "" {
		orders := parseOrderBy(matches[4])
		q.order = orders
	}

	// Parse LIMIT
	if len(matches) > 5 && matches[5] != "" {
		limit, _ := strconv.Atoi(matches[5])
		q.limit = limit
	}

	// Parse OFFSET
	if len(matches) > 6 && matches[6] != "" {
		offset, _ := strconv.Atoi(matches[6])
		q.offset = offset
	}

	return q.Execute()
}

// parseFieldList parses a comma-separated field list
func parseFieldList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "*" {
		return []string{"*"}
	}

	parts := strings.Split(s, ",")
	fields := make([]string, 0, len(parts))
	for _, p := range parts {
		fields = append(fields, strings.TrimSpace(p))
	}
	return fields
}

// parseWhereClause parses a WHERE clause
func parseWhereClause(s string) []Condition {
	var conditions []Condition

	// Split by AND/OR
	re := regexp.MustCompile(`\s+(AND|OR)\s+`)
	parts := re.Split(s, -1)
	logicMatches := re.FindAllStringSubmatch(s, -1)

	for i, part := range parts {
		cond := parseCondition(part)
		if i > 0 && i-1 < len(logicMatches) {
			cond.Logic = logicMatches[i-1][1]
		} else {
			cond.Logic = "AND"
		}
		conditions = append(conditions, cond)
	}

	return conditions
}

// parseCondition parses a single condition
func parseCondition(s string) Condition {
	s = strings.TrimSpace(s)

	// Try various operators
	operators := []string{"<=", ">=", "<>", "!=", "=", "<", ">", " LIKE ", " IN "}

	for _, op := range operators {
		idx := strings.Index(strings.ToUpper(s), strings.ToUpper(op))
		if idx >= 0 {
			column := strings.TrimSpace(s[:idx])
			value := strings.TrimSpace(s[idx+len(op):])

			// Remove quotes from value
			if (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) ||
				(strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) {
				value = value[1 : len(value)-1]
			}

			return Condition{
				Column:   column,
				Operator: strings.TrimSpace(op),
				Value:    value,
			}
		}
	}

	return Condition{}
}

// parseOrderBy parses ORDER BY clause
func parseOrderBy(s string) []OrderBy {
	var orders []OrderBy

	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		desc := strings.HasSuffix(strings.ToUpper(part), " DESC")

		column := strings.TrimSuffix(part, " DESC")
		column = strings.TrimSuffix(column, " desc")
		column = strings.TrimSuffix(column, " ASC")
		column = strings.TrimSuffix(column, " asc")
		column = strings.TrimSpace(column)

		orders = append(orders, OrderBy{Column: column, Desc: desc})
	}

	return orders
}

// executeInsert parses and executes an INSERT statement
func (db *Database) executeInsert(sql string) error {
	re := regexp.MustCompile(`(?i)INSERT\s+INTO\s+(\w+)\s*\((.+?)\)\s*VALUES\s*\((.+?)\)`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 4 {
		return fmt.Errorf("invalid INSERT syntax")
	}

	tableName := matches[1]
	columns := parseFieldList(matches[2])
	valuesStr := matches[3]

	table, err := db.Table(tableName)
	if err != nil {
		return err
	}

	// Parse values
	values := parseValuesList(valuesStr)

	if len(columns) != len(values) {
		return fmt.Errorf("column count doesn't match value count")
	}

	// Build value map
	valueMap := make(map[string]interface{})
	for i, col := range columns {
		valueMap[col] = values[i]
	}

	return table.Insert(valueMap)
}

// parseValuesList parses comma-separated values
func parseValuesList(s string) []interface{} {
	var values []interface{}

	parts := strings.Split(s, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)

		// Remove quotes
		if (strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'")) ||
			(strings.HasPrefix(part, "\"") && strings.HasSuffix(part, "\"")) {
			values = append(values, part[1:len(part)-1])
		} else if strings.ToUpper(part) == "NULL" {
			values = append(values, nil)
		} else if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			values = append(values, n)
		} else if f, err := strconv.ParseFloat(part, 64); err == nil {
			values = append(values, f)
		} else {
			values = append(values, part)
		}
	}

	return values
}

// executeUpdate executes an UPDATE statement
func (db *Database) executeUpdate(sql string) error {
	re := regexp.MustCompile(`(?i)UPDATE\s+(\w+)\s+SET\s+(.+?)(?:\s+WHERE\s+(.+))?$`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 3 {
		return fmt.Errorf("invalid UPDATE syntax")
	}

	tableName := matches[1]
	setClause := matches[2]

	table, err := db.Table(tableName)
	if err != nil {
		return err
	}

	// Parse SET clause
	values := parseSetClause(setClause)

	// Parse WHERE clause
	var whereFn func(*Row) bool
	if len(matches) > 3 && matches[3] != "" {
		conditions := parseWhereClause(matches[3])
		whereFn = func(row *Row) bool {
			for _, cond := range conditions {
				if !evaluateRowCondition(row, cond) {
					return false
				}
			}
			return true
		}
	}

	_, err = table.Update(values, whereFn)
	return err
}

// parseSetClause parses UPDATE SET clause
func parseSetClause(s string) map[string]interface{} {
	values := make(map[string]interface{})

	parts := strings.Split(s, ",")
	for _, part := range parts {
		eqIdx := strings.Index(part, "=")
		if eqIdx < 0 {
			continue
		}

		column := strings.TrimSpace(part[:eqIdx])
		value := strings.TrimSpace(part[eqIdx+1:])

		// Remove quotes
		if (strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) ||
			(strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) {
			values[column] = value[1 : len(value)-1]
		} else if strings.ToUpper(value) == "NULL" {
			values[column] = nil
		} else if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			values[column] = n
		} else if f, err := strconv.ParseFloat(value, 64); err == nil {
			values[column] = f
		} else {
			values[column] = value
		}
	}

	return values
}

// evaluateRowCondition evaluates a condition against a row
func evaluateRowCondition(row *Row, cond Condition) bool {
	value := row.Values[cond.Column]

	switch cond.Operator {
	case "=", "==":
		return compareEqual(value, cond.Value)
	case "!=", "<>":
		return !compareEqual(value, cond.Value)
	default:
		return false
	}
}

// executeDelete executes a DELETE statement
func (db *Database) executeDelete(sql string) error {
	re := regexp.MustCompile(`(?i)DELETE\s+FROM\s+(\w+)(?:\s+WHERE\s+(.+))?$`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 2 {
		return fmt.Errorf("invalid DELETE syntax")
	}

	tableName := matches[1]

	table, err := db.Table(tableName)
	if err != nil {
		return err
	}

	var whereFn func(*Row) bool
	if len(matches) > 2 && matches[2] != "" {
		conditions := parseWhereClause(matches[2])
		whereFn = func(row *Row) bool {
			for _, cond := range conditions {
				if !evaluateRowCondition(row, cond) {
					return false
				}
			}
			return true
		}
	}

	_, err = table.Delete(whereFn)
	return err
}

// executeCreateTable executes a CREATE TABLE statement
func (db *Database) executeCreateTable(sql string) error {
	re := regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(\w+)\s*\((.+)\)`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 3 {
		return fmt.Errorf("invalid CREATE TABLE syntax")
	}

	tableName := matches[1]
	columnDefs := matches[2]

	// Parse column definitions
	columns := parseColumnDefinitions(columnDefs)
	if len(columns) == 0 {
		return fmt.Errorf("no columns defined")
	}

	def := TableDef{
		Name:    tableName,
		Columns: columns,
	}

	_, err := db.CreateTable(def)
	return err
}

// parseColumnDefinitions parses column definitions from CREATE TABLE
func parseColumnDefinitions(s string) []ColumnDef {
	var columns []ColumnDef

	parts := splitColumnDefs(s)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Skip constraints
		upperPart := strings.ToUpper(part)
		if strings.HasPrefix(upperPart, "PRIMARY KEY") ||
			strings.HasPrefix(upperPart, "FOREIGN KEY") ||
			strings.HasPrefix(upperPart, "UNIQUE") ||
			strings.HasPrefix(upperPart, "CHECK") ||
			strings.HasPrefix(upperPart, "CONSTRAINT") {
			continue
		}

		col := parseColumnDef(part)
		if col.Name != "" {
			columns = append(columns, col)
		}
	}

	return columns
}

// splitColumnDefs splits column definitions respecting parentheses
func splitColumnDefs(s string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for _, c := range s {
		if c == '(' {
			depth++
			current.WriteRune(c)
		} else if c == ')' {
			depth--
			current.WriteRune(c)
		} else if c == ',' && depth == 0 {
			parts = append(parts, current.String())
			current.Reset()
		} else {
			current.WriteRune(c)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

// parseColumnDef parses a single column definition
func parseColumnDef(s string) ColumnDef {
	tokens := strings.Fields(s)
	if len(tokens) < 2 {
		return ColumnDef{}
	}

	col := ColumnDef{
		Name:     tokens[0],
		Nullable: true,
	}

	// Parse type
	typeStr := strings.ToUpper(tokens[1])
	col.Type, col.Length = parseColumnType(typeStr)

	// Check for length in parentheses
	if len(tokens) > 2 && strings.HasPrefix(tokens[2], "(") {
		lenStr := strings.Trim(tokens[2], "()")
		if l, err := strconv.Atoi(lenStr); err == nil {
			col.Length = l
		}
	}

	// Check for modifiers
	fullDef := strings.ToUpper(s)
	if strings.Contains(fullDef, "NOT NULL") {
		col.Nullable = false
	}
	if strings.Contains(fullDef, "AUTOINCREMENT") ||
		strings.Contains(fullDef, "AUTO_INCREMENT") ||
		strings.Contains(fullDef, "IDENTITY") {
		col.AutoIncrement = true
	}
	if strings.Contains(fullDef, "PRIMARY KEY") {
		col.Nullable = false
	}

	return col
}

// parseColumnType parses SQL type to internal type
func parseColumnType(s string) (ColumnType, int) {
	if idx := strings.Index(s, "("); idx >= 0 {
		lenPart := s[idx+1:]
		if endIdx := strings.Index(lenPart, ")"); endIdx >= 0 {
			lenStr := lenPart[:endIdx]
			s = s[:idx]
			if l, err := strconv.Atoi(lenStr); err == nil {
				return mapSQLType(s), l
			}
		}
	}

	return mapSQLType(s), 0
}

// mapSQLType maps SQL type names to internal types
func mapSQLType(s string) ColumnType {
	s = strings.ToUpper(strings.TrimSpace(s))

	switch s {
	case "BIT", "BOOLEAN", "BOOL", "YESNO":
		return ColTypeBoolean
	case "TINYINT", "BYTE":
		return ColTypeByte
	case "SMALLINT", "SHORT", "INT2":
		return ColTypeInt
	case "INT", "INTEGER", "LONG", "INT4":
		return ColTypeLongInt
	case "BIGINT", "INT8":
		return ColTypeBigInt
	case "MONEY", "CURRENCY":
		return ColTypeMoney
	case "REAL", "SINGLE", "FLOAT4":
		return ColTypeFloat
	case "FLOAT", "DOUBLE", "FLOAT8", "NUMBER":
		return ColTypeDouble
	case "DATETIME", "DATE", "TIME", "TIMESTAMP":
		return ColTypeDateTime
	case "TEXT", "VARCHAR", "CHAR", "NVARCHAR", "NCHAR", "STRING":
		return ColTypeText
	case "MEMO", "LONGTEXT", "NTEXT", "CLOB":
		return ColTypeMemo
	case "BINARY", "VARBINARY", "BLOB", "IMAGE":
		return ColTypeBinary
	case "OLE", "OLEOBJECT":
		return ColTypeOLE
	case "GUID", "UNIQUEIDENTIFIER":
		return ColTypeGUID
	default:
		return ColTypeText
	}
}

// executeDropTable executes a DROP TABLE statement
func (db *Database) executeDropTable(sql string) error {
	re := regexp.MustCompile(`(?i)DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?(\w+)`)
	matches := re.FindStringSubmatch(sql)

	if len(matches) < 2 {
		return fmt.Errorf("invalid DROP TABLE syntax")
	}

	tableName := matches[1]

	ifExists := strings.Contains(strings.ToUpper(sql), "IF EXISTS")

	if !db.HasTable(tableName) {
		if ifExists {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrTableNotFound, tableName)
	}

	return db.DropTable(tableName)
}
