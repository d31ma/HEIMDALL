package azurefunctions

type optionalValue[T any] struct{}

func (optionalValue[T]) read(item *T) T {
	if item != nil {
		return *item
	}
	var empty T
	return empty
}
