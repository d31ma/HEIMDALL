package provider

type InvocationRequest struct {
	Target  Target
	App     AppRef
	Service string
	Payload []byte
}
