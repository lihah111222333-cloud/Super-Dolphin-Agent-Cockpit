package orchestration

func newDAGTestService(p dagControllerParams) *service {
	return attachDAGTestController(&service{}, p)
}

func attachDAGTestController(svc *service, p dagControllerParams) *service {
	if svc == nil {
		svc = &service{}
	}
	if p.Logger == nil {
		p.Logger = svc.logger
	}
	if p.EventBus == nil {
		p.EventBus = svc.eventBus
	}
	if p.SvcStopper == nil {
		p.SvcStopper = svc
	}
	svc.dagController = newDAGController(p)
	return svc
}
