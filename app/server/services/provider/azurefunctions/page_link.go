package azurefunctions

import (
	"net/url"
	"strings"

	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type pageLink struct {
	baseText string
	nextText string
	base     *url.URL
	next     *url.URL
}

func (pageLink) resolve(base, next string) (string, error) {
	link := pageLink{baseText: base, nextText: next}
	err := (sequence.Steps{Actions: []func() error{link.parseBase, link.parseNext, link.resolveNext, link.confine}}).Run()
	return link.result(err)
}
func (p *pageLink) parseBase() error { base, err := url.Parse(p.baseText); p.base = base; return err }
func (p *pageLink) parseNext() error { next, err := url.Parse(p.nextText); p.next = next; return err }
func (p *pageLink) confine() error {
	if p.nextText != "" && (p.next.Scheme != p.base.Scheme || !strings.EqualFold(p.next.Host, p.base.Host) || p.next.User != nil || p.next.Path != p.base.Path || p.next.Query().Get("api-version") != (functionContract{}).apiVersion()) {
		return &AdapterError{Code: (functionContract{}).readCode(), Message: "ARM pagination escaped the target collection or API version"}
	}
	return nil
}
func (p pageLink) result(err error) (string, error) {
	if err != nil || p.nextText == "" {
		return "", err
	}
	return p.next.String(), nil
}

func (p *pageLink) resolveNext() error { p.next = p.base.ResolveReference(p.next); return nil }
