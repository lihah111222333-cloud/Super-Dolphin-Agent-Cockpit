package gate

func knownDerivedFact() DurationLedgerDerivedFact {
	return DurationLedgerDerivedFact{Status: DurationLedgerDerivedKnown}
}

func unknownDerivedFact(reason string) DurationLedgerDerivedFact {
	return DurationLedgerDerivedFact{Status: DurationLedgerDerivedUnknown, Reason: reason}
}

func unknownDerivedCompleteness(reason string) DurationLedgerDerivedCompleteness {
	fact := unknownDerivedFact(reason)
	return DurationLedgerDerivedCompleteness{Overall: fact, PhaseGateRun: fact, RetryCost: fact, CancellationCost: fact, PreV6Completeness: fact, StoredFormulaVersion: fact, LiveWarningHistory: fact, UnavailableCapacity: fact}
}

func unknownDerivedMeasurement(reason string) DurationLedgerDerivedMeasurement {
	return DurationLedgerDerivedMeasurement{Status: DurationLedgerDerivedUnknown, Reason: reason}
}
