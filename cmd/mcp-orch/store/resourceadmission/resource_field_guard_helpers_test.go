package resourceadmission

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type resourceFieldChain struct {
	producers          map[string]any
	consumers          map[string]any
	edges              map[string]string
	producerExemptions map[string]string
	consumerExemptions map[string]string
}

type resourceFieldDrift struct {
	missingProducers  []string
	missingConsumers  []string
	staleEdgeSources  []string
	staleEdgeTargets  []string
	invalidProducerEx []string
	invalidConsumerEx []string
}

func resourceAssertFieldChain(t *testing.T, chain resourceFieldChain) {
	t.Helper()
	drift := resourceFieldChainDifference(chain)
	if resourceFieldDriftCount(drift) != 0 {
		t.Fatalf("resource field chain drifted: missing_producers=%v missing_consumers=%v stale_edge_sources=%v stale_edge_targets=%v invalid_producer_exemptions=%v invalid_consumer_exemptions=%v",
			drift.missingProducers, drift.missingConsumers, drift.staleEdgeSources, drift.staleEdgeTargets,
			drift.invalidProducerEx, drift.invalidConsumerEx)
	}
	for source, target := range chain.edges {
		if !reflect.DeepEqual(chain.producers[source], chain.consumers[target]) {
			t.Errorf("resource field edge %s -> %s mapped %#v, want %#v",
				source, target, chain.consumers[target], chain.producers[source])
		}
	}
}

func resourceFieldDriftCount(drift resourceFieldDrift) int {
	return len(drift.missingProducers) + len(drift.missingConsumers) + len(drift.staleEdgeSources) +
		len(drift.staleEdgeTargets) + len(drift.invalidProducerEx) + len(drift.invalidConsumerEx)
}

func resourceFieldChainDifference(chain resourceFieldChain) resourceFieldDrift {
	drift := resourceFieldDrift{}
	covered := make(map[string]bool, len(chain.edges))
	for source, target := range chain.edges {
		if _, ok := chain.producers[source]; !ok {
			drift.staleEdgeSources = append(drift.staleEdgeSources, source)
		}
		if _, ok := chain.consumers[target]; !ok {
			drift.staleEdgeTargets = append(drift.staleEdgeTargets, target)
		}
		covered[target] = true
	}
	resourceFindMissing(chain.producers, chain.edges, chain.producerExemptions, &drift.missingProducers)
	resourceFindMissingConsumers(chain.consumers, covered, chain.consumerExemptions, &drift.missingConsumers)
	drift.invalidProducerEx = resourceInvalidExemptions(chain.producers, chain.edges, chain.producerExemptions)
	drift.invalidConsumerEx = resourceInvalidConsumerExemptions(chain.consumers, covered, chain.consumerExemptions)
	resourceSortFieldDrift(&drift)
	return drift
}

func resourceFindMissing(fields map[string]any, edges map[string]string, exemptions map[string]string, missing *[]string) {
	for field := range fields {
		if edges[field] == "" && exemptions[field] == "" {
			*missing = append(*missing, field)
		}
	}
}

func resourceFindMissingConsumers(fields map[string]any, covered map[string]bool, exemptions map[string]string, missing *[]string) {
	for field := range fields {
		if !covered[field] && exemptions[field] == "" {
			*missing = append(*missing, field)
		}
	}
}

func resourceInvalidExemptions(fields map[string]any, edges map[string]string, exemptions map[string]string) []string {
	invalid := make([]string, 0)
	for field, reason := range exemptions {
		if _, exists := fields[field]; !exists || edges[field] != "" || !resourceSpecificExemption(reason) {
			invalid = append(invalid, field)
		}
	}
	return invalid
}

func resourceInvalidConsumerExemptions(fields map[string]any, covered map[string]bool, exemptions map[string]string) []string {
	invalid := make([]string, 0)
	for field, reason := range exemptions {
		if _, exists := fields[field]; !exists || covered[field] || !resourceSpecificExemption(reason) {
			invalid = append(invalid, field)
		}
	}
	return invalid
}

func resourceSpecificExemption(reason string) bool {
	parts := strings.SplitN(strings.TrimSpace(reason), ":", 2)
	return len(parts) == 2 && strings.HasSuffix(strings.ToLower(strings.TrimSpace(parts[0])), "owner") &&
		len(strings.TrimSpace(parts[1])) >= 16
}

func resourceSortFieldDrift(drift *resourceFieldDrift) {
	sort.Strings(drift.missingProducers)
	sort.Strings(drift.missingConsumers)
	sort.Strings(drift.staleEdgeSources)
	sort.Strings(drift.staleEdgeTargets)
	sort.Strings(drift.invalidProducerEx)
	sort.Strings(drift.invalidConsumerEx)
}

func resourceStructFields(t *testing.T, prefix string, value any) map[string]any {
	t.Helper()
	fields := make(map[string]any)
	resourceFlattenStruct(t, reflect.ValueOf(value), prefix, fields)
	if len(fields) == 0 {
		t.Fatalf("%s has no exported leaf fields", prefix)
	}
	return fields
}

func resourceFlattenStruct(t *testing.T, value reflect.Value, prefix string, fields map[string]any) {
	t.Helper()
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	valueType := value.Type()
	for index := 0; index < valueType.NumField(); index++ {
		field := valueType.Field(index)
		if !field.IsExported() {
			continue
		}
		path := prefix + "." + field.Name
		fieldValue := value.Field(index)
		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeFor[time.Time]() {
			resourceFlattenStruct(t, fieldValue, path, fields)
			continue
		}
		if _, exists := fields[path]; exists {
			t.Fatalf("duplicate reflected resource field %s", path)
		}
		fields[path] = resourceComparableValue(fieldValue.Interface())
	}
}

func resourceComparableValue(value any) any {
	if instant, ok := value.(time.Time); ok {
		return instant.UTC().Format(time.RFC3339Nano)
	}
	return value
}

func resourceMergeFields(t *testing.T, sets ...map[string]any) map[string]any {
	t.Helper()
	merged := make(map[string]any)
	for _, fields := range sets {
		for field, value := range fields {
			if _, exists := merged[field]; exists {
				t.Fatalf("duplicate merged resource field %s", field)
			}
			merged[field] = value
		}
	}
	return merged
}

func resourceSelectEdgeSources(t *testing.T, all map[string]any, edges map[string]string, prefix string) map[string]any {
	t.Helper()
	selected := make(map[string]any)
	for source := range edges {
		if !strings.HasPrefix(source, prefix+".") {
			continue
		}
		value, exists := all[source]
		if !exists {
			t.Fatalf("resource edge source %s is absent from reflected producer", source)
		}
		selected[source] = value
	}
	return selected
}

func resourceSameNameEdges(producers, consumers map[string]any, producerPrefix, consumerPrefix string) map[string]string {
	edges := make(map[string]string)
	for source := range producers {
		suffix := strings.TrimPrefix(source, producerPrefix+".")
		target := consumerPrefix + "." + suffix
		if _, exists := consumers[target]; exists {
			edges[source] = target
		}
	}
	return edges
}
