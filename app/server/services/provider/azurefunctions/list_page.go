package azurefunctions

type listPage struct {
	Value    []*nativeApp `json:"value"`
	NextLink string       `json:"nextLink"`
}
