package lambda

import "strconv"

type immutableVersion struct{ value string }

func (version immutableVersion) Valid() bool {
	number, failure := strconv.Atoi(version.value)
	return version.valid(number, failure)
}
func (immutableVersion) valid(number int, failure error) bool { return failure == nil && number >= 1 }
