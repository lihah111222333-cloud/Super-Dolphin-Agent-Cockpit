package typednilfixture

type worker struct{}

func (*worker) run() int { return 0 }

type runner interface {
	run() int
}

var _ runner = (*worker)(nil)

func badReturn() any {
	return (*worker)(nil) // want "typed nil .* converted to interface"
}

func badAssignment() {
	var value any
	value = (*worker)(nil) // want "typed nil .* converted to interface"
	_ = value
}

func badValueSpec() {
	var value any = (*worker)(nil) // want "typed nil .* converted to interface"
	_ = value
}

func badCompositeLiterals() {
	_ = []any{(*worker)(nil)}                        // want "typed nil .* converted to interface"
	_ = map[string]any{"worker": (*worker)(nil)}     // want "typed nil .* converted to interface"
	_ = struct{ worker any }{worker: (*worker)(nil)} // want "typed nil .* converted to interface"
}

func receiveAny(values ...any) {
	_ = values
}

func badVariadicCall() {
	receiveAny((*worker)(nil)) // want "typed nil .* converted to interface"
}

func badDefinitelyNilVariable() any {
	var value *worker
	return value // want "typed nil .* converted to interface"
}

func goodNilInterface() any {
	return nil
}

func goodConcreteValue() any {
	return &worker{}
}
