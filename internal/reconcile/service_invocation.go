package reconcile

type ServiceInvocation struct {
	AppID   string
	Service string
	Payload []byte
}
