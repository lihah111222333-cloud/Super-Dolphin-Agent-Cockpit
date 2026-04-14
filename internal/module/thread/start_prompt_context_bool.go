package thread

func firstNonNilBool(primary, fallback *bool) *bool {
	if primary != nil {
		return primary
	}
	return fallback
}

func cloneOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
