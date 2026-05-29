package domain

// IsValidPaymentCode reports whether code is exactly five numeric digits.
func IsValidPaymentCode(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
