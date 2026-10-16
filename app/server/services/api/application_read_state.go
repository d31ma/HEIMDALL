package api

import "github.com/d31ma/heimdall/app/server/repositories/store"

type applicationReadState struct {
	response  applicationResponse
	revisions []store.Revision
	failure   error
}
