package contract

type HandlerMap map[string]any

type HandlerProvider interface {
	HandlerMap() HandlerMap
}
