package newznab

import (
	"bytes"
	"net/mail"
	"regexp"
	"strconv"
	"time"

	"github.com/media-manager/media-manager/internal/indexers"
)

// Some indexers state their own usage counters in every answer:
//
//	<newznab:apilimits apicurrent="2912" apimax="10000" grabcurrent="299"
//	    grabmax="300" apioldesttime="…" graboldesttime="…"/>
//
// ParseProviderLimits reads that element. Nothing else in the answer is used,
// and a missing or malformed element simply yields no reading.

var (
	apiLimitsElement   = regexp.MustCompile(`(?i)<(?:[a-z0-9]+:)?apilimits\s([^<>]{0,1000})>`)
	apiLimitsAttribute = regexp.MustCompile(`(?i)([a-z]+)\s*=\s*"([^"]{0,100})"`)
)

// providerLimitsScan bounds how much of an answer is searched: the element
// sits in the channel header, before the first item.
const providerLimitsScan = 16 << 10

func ParseProviderLimits(body []byte, observedAt time.Time) (indexers.ProviderLimits, bool) {
	limits := indexers.ProviderLimits{ObservedAt: observedAt.UTC(), QueryUsed: -1, QueryMax: -1, GrabUsed: -1, GrabMax: -1}
	if len(body) > providerLimitsScan {
		body = body[:providerLimitsScan]
	}
	if !bytes.Contains(bytes.ToLower(body), []byte("apilimits")) {
		return limits, false
	}
	element := apiLimitsElement.FindSubmatch(body)
	if element == nil {
		return limits, false
	}
	for _, attribute := range apiLimitsAttribute.FindAllSubmatch(element[1], 16) {
		name, value := string(bytes.ToLower(attribute[1])), string(bytes.TrimSpace(attribute[2]))
		switch name {
		case "apicurrent":
			limits.QueryUsed = providerCount(value)
		case "apimax":
			limits.QueryMax = providerCount(value)
		case "grabcurrent":
			limits.GrabUsed = providerCount(value)
		case "grabmax":
			limits.GrabMax = providerCount(value)
		case "apioldesttime":
			limits.QueryOldest = providerTime(value)
		case "graboldesttime":
			limits.GrabOldest = providerTime(value)
		}
	}
	return limits, limits.Valid()
}

func providerCount(value string) int {
	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return -1
	}
	return number
}

func providerTime(value string) time.Time {
	parsed, err := mail.ParseDate(value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
