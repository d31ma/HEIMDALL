package azurefunctions

type resourceTags struct{}

func (resourceTags) key(name string) string { return "dev.delma.heimdall." + name }
func (t resourceTags) value(tags map[string]*string, name string) string {
	return (optionalValue[string]{}).read(tags[t.key(name)])
}
