package azurefunctions

type revisionImage struct{}

func (revisionImage) read(revision *nativeRevision) string {
	properties := (revisionComparison{}).value(revision)
	container := (templateComparison{}).container(properties.Template)
	value := (containerComparison{}).value(container)
	return (optionalValue[string]{}).read(value.Image)
}
func (revisionImage) message(revision *nativeRevision) string {
	properties := (revisionComparison{}).value(revision)
	container := (templateComparison{}).container(properties.Template)
	return (revisionImage{}).description(container != nil)
}
func (revisionImage) description(observed bool) string {
	if observed {
		return "native deployment health and immutable ready revision image; execution inventory is not observed by this adapter"
	}
	return "native deployment health; ready revision image is unobserved; execution inventory is not observed by this adapter"
}

func (r revisionImage) version(revision *nativeRevision) string {
	value := r.value(revision)
	return (optionalValue[string]{}).read(value.Name)
}
func (revisionImage) value(revision *nativeRevision) nativeRevision {
	if revision == nil {
		return nativeRevision{}
	}
	return *revision
}
