package content

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type contentFieldChain struct {
	producers          map[string]any
	consumers          map[string]any
	edges              map[string][]string
	producerExemptions map[string]string
	consumerExemptions map[string]string
}

type contentFieldChainDrift struct {
	missingProducers  []string
	missingConsumers  []string
	staleEdgeSources  []string
	staleEdgeTargets  []string
	invalidProducerEx []string
	invalidConsumerEx []string
}

func contentAssertFieldChain(t *testing.T, chain contentFieldChain) {
	t.Helper()
	drift := contentFieldChainDifference(chain)
	if !reflect.DeepEqual(drift, contentFieldChainDrift{}) {
		t.Fatalf("content field chain drifted: missing_producers=%v missing_consumers=%v stale_edge_sources=%v stale_edge_targets=%v invalid_producer_exemptions=%v invalid_consumer_exemptions=%v",
			drift.missingProducers, drift.missingConsumers, drift.staleEdgeSources, drift.staleEdgeTargets,
			drift.invalidProducerEx, drift.invalidConsumerEx)
	}
}

func contentFieldChainDifference(chain contentFieldChain) contentFieldChainDrift {
	drift := contentFieldChainDrift{}
	coveredConsumers := make(map[string]bool)
	for source, targets := range chain.edges {
		if _, ok := chain.producers[source]; !ok {
			drift.staleEdgeSources = append(drift.staleEdgeSources, source)
		}
		for _, target := range targets {
			coveredConsumers[target] = true
			if _, ok := chain.consumers[target]; !ok {
				drift.staleEdgeTargets = append(drift.staleEdgeTargets, target)
			}
		}
	}
	contentFindMissing(chain.producers, chain.edges, chain.producerExemptions, &drift.missingProducers)
	contentFindMissingConsumers(chain.consumers, coveredConsumers, chain.consumerExemptions, &drift.missingConsumers)
	drift.invalidProducerEx = contentInvalidChainExemptions(chain.producers, chain.edges, chain.producerExemptions)
	drift.invalidConsumerEx = contentInvalidChainExemptions(chain.consumers, contentConsumerEdges(coveredConsumers), chain.consumerExemptions)
	contentSortFieldChainDrift(&drift)
	return drift
}

func contentFindMissing(fields map[string]any, edges map[string][]string, exemptions map[string]string, missing *[]string) {
	for field := range fields {
		if len(edges[field]) == 0 && strings.TrimSpace(exemptions[field]) == "" {
			*missing = append(*missing, field)
		}
	}
}

func contentFindMissingConsumers(fields map[string]any, covered map[string]bool, exemptions map[string]string, missing *[]string) {
	for field := range fields {
		if !covered[field] && strings.TrimSpace(exemptions[field]) == "" {
			*missing = append(*missing, field)
		}
	}
}

func contentInvalidChainExemptions(fields map[string]any, edges map[string][]string, exemptions map[string]string) []string {
	var invalid []string
	for field, reason := range exemptions {
		_, exists := fields[field]
		if !exists || len(edges[field]) != 0 || !contentSpecificExemptionReason(reason) {
			invalid = append(invalid, field)
		}
	}
	return invalid
}

func contentSpecificExemptionReason(reason string) bool {
	trimmed := strings.TrimSpace(reason)
	return len(trimmed) >= 24 && strings.Contains(trimmed, ":") &&
		!strings.Contains(strings.ToLower(trimmed), "temporary") &&
		!strings.Contains(strings.ToLower(trimmed), "later")
}

func contentConsumerEdges(covered map[string]bool) map[string][]string {
	edges := make(map[string][]string, len(covered))
	for field := range covered {
		edges[field] = []string{field}
	}
	return edges
}

func contentSortFieldChainDrift(drift *contentFieldChainDrift) {
	sort.Strings(drift.missingProducers)
	sort.Strings(drift.missingConsumers)
	sort.Strings(drift.staleEdgeSources)
	sort.Strings(drift.staleEdgeTargets)
	sort.Strings(drift.invalidProducerEx)
	sort.Strings(drift.invalidConsumerEx)
}

func contentStructFields(t *testing.T, prefix string, value any) map[string]any {
	t.Helper()
	fields := make(map[string]any)
	contentFlattenStruct(t, reflect.ValueOf(value), prefix, fields)
	if len(fields) == 0 {
		t.Fatalf("%s producer/consumer type has no exported fields", prefix)
	}
	return fields
}

func contentFlattenStruct(t *testing.T, value reflect.Value, prefix string, fields map[string]any) {
	t.Helper()
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	typeOfValue := value.Type()
	for index := 0; index < typeOfValue.NumField(); index++ {
		field := typeOfValue.Field(index)
		if !field.IsExported() {
			continue
		}
		path := prefix + "." + field.Name
		fieldValue := value.Field(index)
		if field.Anonymous {
			contentFlattenStruct(t, fieldValue, prefix, fields)
			continue
		}
		if field.Type.Kind() == reflect.Struct && field.Type != reflect.TypeFor[time.Time]() {
			contentFlattenStruct(t, fieldValue, path, fields)
			continue
		}
		if _, exists := fields[path]; exists {
			t.Fatalf("duplicate reflected field path %q", path)
		}
		fields[path] = fieldValue.Interface()
	}
}

func contentMergeFieldSets(t *testing.T, sets ...map[string]any) map[string]any {
	t.Helper()
	merged := make(map[string]any)
	for _, fields := range sets {
		for field, value := range fields {
			if _, exists := merged[field]; exists {
				t.Fatalf("duplicate merged field path %q", field)
			}
			merged[field] = value
		}
	}
	return merged
}

func contentAssertDirectEdges(t *testing.T, producers, consumers map[string]any, edges map[string][]string) {
	t.Helper()
	for source, targets := range edges {
		for _, target := range targets {
			if !reflect.DeepEqual(producers[source], consumers[target]) {
				t.Errorf("field edge %s -> %s mapped %#v, want %#v", source, target, consumers[target], producers[source])
			}
		}
	}
}

func testContentFieldChainDifferenceRejectsBothDirections(t *testing.T) {
	base := contentFieldChain{
		producers: map[string]any{"source.present": 1}, consumers: map[string]any{"target.present": 1},
		edges:              map[string][]string{"source.present": {"target.present"}},
		producerExemptions: map[string]string{}, consumerExemptions: map[string]string{},
	}
	contentAssertFieldChain(t, base)
	broken := base
	broken.edges = map[string][]string{"source.stale": {"target.stale"}}
	drift := contentFieldChainDifference(broken)
	if !reflect.DeepEqual(drift.missingProducers, []string{"source.present"}) ||
		!reflect.DeepEqual(drift.missingConsumers, []string{"target.present"}) ||
		!reflect.DeepEqual(drift.staleEdgeSources, []string{"source.stale"}) ||
		!reflect.DeepEqual(drift.staleEdgeTargets, []string{"target.stale"}) {
		t.Fatalf("bidirectional field-chain failure detection drifted: %#v", drift)
	}
	invalid := base
	invalid.producerExemptions = map[string]string{"source.present": ""}
	invalid.consumerExemptions = map[string]string{
		"target.present": "consumer owner: mapped fields cannot also claim an exemption",
	}
	drift = contentFieldChainDifference(invalid)
	if !reflect.DeepEqual(drift.invalidProducerEx, []string{"source.present"}) ||
		!reflect.DeepEqual(drift.invalidConsumerEx, []string{"target.present"}) {
		t.Fatalf("bidirectional field-chain exemption detection drifted: %#v", drift)
	}
}
