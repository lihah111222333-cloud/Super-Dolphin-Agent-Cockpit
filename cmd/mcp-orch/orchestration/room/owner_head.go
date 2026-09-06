package room

// Head 返回候选 admission head 快照；它不是 durable read 或执行许可。
func (o *Owner) Head() AdmissionHead {
	if o == nil {
		return AdmissionHead{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.arbiter.Head()
}
