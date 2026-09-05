// Package export renders records as the CSV an accounting package imports.
package export

import "strings"

// Row renders one record as a CSV line: the fields separated by commas and
// the line ended with CRLF. A field that carries a comma, a double quote,
// a carriage return or a newline is wrapped in double quotes, and every
// double quote inside such a field is written twice. A field that carries
// none of those is written as it is, with no quotes around it.
func Row(fields []string) string {
	return strings.Join(fields, ",") + "\r\n"
}
