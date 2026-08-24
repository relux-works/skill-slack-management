package securefs

// RestrictFile applies the strongest available current-user-only protection to
// an existing file.
func RestrictFile(path string) error {
	return restrictCurrentUser(path, false)
}

// RestrictDirectory applies current-user-only protection to an existing
// directory and, where supported, makes newly created descendants inherit it.
func RestrictDirectory(path string) error {
	return restrictCurrentUser(path, true)
}

// ValidateFile reports whether an existing file has the current-user-only
// protection RestrictFile promises, without modifying it.
func ValidateFile(path string) error {
	return validateCurrentUser(path, false)
}

// ValidateDirectory reports whether an existing directory has the
// current-user-only protection RestrictDirectory promises, without modifying
// it.
func ValidateDirectory(path string) error {
	return validateCurrentUser(path, true)
}
