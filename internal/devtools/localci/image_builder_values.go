package localci

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func validateImmutableReference(name string, reference string) error {
	separator := strings.LastIndex(reference, "@")
	if separator <= 0 {
		return fmt.Errorf("%s must use an immutable repository@sha256 reference", name)
	}
	return validateDigest(name, reference[separator+1:])
}

func validateSourceDateEpoch(value string) error {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || strconv.FormatInt(seconds, 10) != value {
		return errors.New("source_date_epoch must be a canonical non-negative integer")
	}
	return nil
}

func validateSortedUnique(name string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", name)
	}
	previous := ""
	for _, value := range values {
		if value == "" || value <= previous {
			return fmt.Errorf("%s must be non-empty, unique, and sorted", name)
		}
		previous = value
	}
	return nil
}

func containsString(values []string, wanted string) bool { return slices.Contains(values, wanted) }

func runtimeDepsInputsDigest(inputs runtimeDepsInputs) string {
	fields := make([]string, 0, 28)
	for _, binding := range runtimeDepsInputBindings(inputs) {
		fields = append(fields, binding.path, binding.digest)
	}
	return fieldsDigest(fields...)
}
