package tools

import (
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"rpeek/internal/dbconn"
)

// dbLookup resolves a database alias against the server's registry. It fails closed: when
// no databases are configured, or the alias is missing or unknown, it returns an error. The
// unknown-alias error enumerates the configured aliases, which are not secret — only the DSN
// is — so the client learns what it may query. The alias is matched case-insensitively,
// since both configuration sources register it lower-cased.
func dbLookup(env Env, alias string) (*dbconn.Conn, error) {
	if env.DB == nil || len(env.DB.Aliases()) == 0 {
		return nil, fmt.Errorf("no databases are configured on this server")
	}
	if alias == "" {
		return nil, fmt.Errorf("a database alias is required (--db); configured: %s",
			strings.Join(env.DB.Aliases(), ", "))
	}
	conn, ok := env.DB.Lookup(strings.ToLower(alias))
	if !ok {
		return nil, fmt.Errorf("unknown db alias %q; configured: %s",
			alias, strings.Join(env.DB.Aliases(), ", "))
	}
	return conn, nil
}

// scanRows reads every row of rs into strings, returning the column names and the rows. NULL
// renders as "NULL"; byte-slice values (many drivers return text and decimals this way)
// render as their string form. Newlines and tabs within a value are folded to spaces so a
// single value cannot break the table layout.
func scanRows(rs *sql.Rows) (cols []string, rows [][]string, err error) {
	cols, err = rs.Columns()
	if err != nil {
		return nil, nil, err
	}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = formatCell(v)
		}
		rows = append(rows, row)
	}
	if err := rs.Err(); err != nil {
		return nil, nil, err
	}
	return cols, rows, nil
}

// formatCell renders one scanned value as a single-line string.
func formatCell(v any) string {
	var s string
	switch t := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		s = string(t)
	case string:
		s = t
	default:
		s = fmt.Sprint(t)
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

// renderTable formats columns and rows as a compact, space-aligned text table with a header
// and an underline. With no rows it still prints the header, so an empty result shows the
// query's shape.
func renderTable(cols []string, rows [][]string) string {
	if len(cols) == 0 {
		return ""
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c)
	}
	for _, r := range rows {
		for i, cell := range r {
			if w := utf8.RuneCountInString(cell); i < len(widths) && w > widths[i] {
				widths[i] = w
			}
		}
	}

	var b strings.Builder
	writeTableRow(&b, cols, widths)
	seps := make([]string, len(cols))
	for i := range seps {
		seps[i] = strings.Repeat("-", widths[i])
	}
	writeTableRow(&b, seps, widths)
	for _, r := range rows {
		writeTableRow(&b, r, widths)
	}
	return b.String()
}

// writeTableRow writes one table row, padding every cell but the last to its column width.
func writeTableRow(b *strings.Builder, cells []string, widths []int) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteString("  ")
		}
		b.WriteString(cell)
		if i < len(cells)-1 {
			if pad := widths[i] - utf8.RuneCountInString(cell); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
	}
	b.WriteByte('\n')
}
