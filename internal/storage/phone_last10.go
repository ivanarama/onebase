package storage

// PhoneLast10SQL uses the same immutable expression in index definitions and
// query predicates. column is a resolved SQL column, optionally qualified.
func PhoneLast10SQL(column string) string {
	digits := column
	for _, separator := range " ()-+." {
		digits = "REPLACE(" + digits + ", '" + string(separator) + "', '')"
	}
	nonDigits := digits
	for _, digit := range "0123456789" {
		nonDigits = "REPLACE(" + nonDigits + ", '" + string(digit) + "', '')"
	}
	length := "LENGTH(" + digits + ")"
	return "CASE WHEN " + nonDigits + " = '' AND (" + length + " = 10 OR (" +
		length + " = 11 AND SUBSTR(" + digits + ", 1, 1) IN ('7', '8'))) THEN SUBSTR(" +
		digits + ", " + length + " - 9, 10) END"
}
