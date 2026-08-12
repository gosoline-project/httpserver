package httpserver

import (
	"encoding/xml"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/encoding/json"
)

// ResponseRepresentation describes one media type that the HTTP server can
// produce from a typed handler result.
type ResponseRepresentation struct {
	MediaType string
	Encode    func(any) ([]byte, error)
}

// ResponseNegotiator converts a typed handler result into a negotiated HTTP
// response using the request's Accept header.
type ResponseNegotiator interface {
	Render(*http.Request, any) (Response, error)
}

type configuredRepresentation struct {
	ResponseRepresentation
	mediaType string
}

// ContentNegotiator selects and invokes one of a configured set of response
// representations.
type ContentNegotiator struct {
	representations  []configuredRepresentation
	defaultMediaType string
}

// NewContentNegotiator creates a response negotiator. MediaType values must be
// concrete media types such as application/json or application/xml; wildcards
// are only valid in the request Accept header. Accept matching considers the
// media type, type/subtype wildcards, and q-values.
func NewContentNegotiator(defaultMediaType string, representations ...ResponseRepresentation) (*ContentNegotiator, error) {
	var err error

	defaultMediaType, err = parseConfiguredMediaType(defaultMediaType)
	if err != nil {
		return nil, fmt.Errorf("invalid default response media type: %w", err)
	}
	if len(representations) == 0 {
		return nil, errors.New("at least one response representation is required")
	}

	configured := make([]configuredRepresentation, 0, len(representations))
	seen := make(map[string]struct{}, len(representations))
	for _, representation := range representations {
		mediaType, mediaTypeErr := parseConfiguredMediaType(representation.MediaType)
		if mediaTypeErr != nil {
			return nil, fmt.Errorf("invalid response media type %q: %w", representation.MediaType, mediaTypeErr)
		}
		if representation.Encode == nil {
			return nil, fmt.Errorf("response representation %q has no encoder", representation.MediaType)
		}
		if _, ok := seen[mediaType]; ok {
			return nil, fmt.Errorf("response media type %q is configured more than once", mediaType)
		}

		seen[mediaType] = struct{}{}
		configured = append(configured, configuredRepresentation{
			ResponseRepresentation: ResponseRepresentation{
				MediaType: mediaType,
				Encode:    representation.Encode,
			},
			mediaType: mediaType,
		})
	}
	if _, ok := seen[defaultMediaType]; !ok {
		return nil, fmt.Errorf("default response media type %q is not configured", defaultMediaType)
	}

	return &ContentNegotiator{
		representations:  configured,
		defaultMediaType: defaultMediaType,
	}, nil
}

// NewDefaultResponseNegotiator creates the server default. JSON is the only
// default response representation; other formats must be registered explicitly.
func NewDefaultResponseNegotiator() ResponseNegotiator {
	negotiator, err := NewContentNegotiator(
		ContentTypeApplicationJson,
		JSONRepresentation(),
	)
	if err != nil {
		panic(err)
	}

	return negotiator
}

// JSONRepresentation returns the standard application/json representation.
func JSONRepresentation() ResponseRepresentation {
	return ResponseRepresentation{
		MediaType: ContentTypeApplicationJson,
		Encode:    json.Marshal,
	}
}

// XMLRepresentation returns the standard application/xml representation.
func XMLRepresentation() ResponseRepresentation {
	return ResponseRepresentation{
		MediaType: ContentTypeApplicationXml,
		Encode:    xml.Marshal,
	}
}

// ResponseNegotiationMiddleware installs a response negotiator for Bind and
// BindN handlers in the current router group.
func ResponseNegotiationMiddleware(negotiator ResponseNegotiator) gin.HandlerFunc {
	if negotiator == nil {
		panic("response negotiator is required")
	}

	return func(ginCtx *gin.Context) {
		ginCtx.Set(responseNegotiatorContextKey, negotiator)
		ginCtx.Next()
	}
}

const responseNegotiatorContextKey = "httpserver.response-negotiator"

var defaultResponseNegotiator = NewDefaultResponseNegotiator()

func responseNegotiatorFromContext(ginCtx *gin.Context) ResponseNegotiator {
	if negotiator, ok := ginCtx.Get(responseNegotiatorContextKey); ok {
		if responseNegotiator, ok := negotiator.(ResponseNegotiator); ok {
			return responseNegotiator
		}
	}

	return defaultResponseNegotiator
}

func (n *ContentNegotiator) Render(request *http.Request, value any) (Response, error) {
	if request == nil {
		return nil, errors.New("request is required for response negotiation")
	}

	accept := strings.Join(request.Header.Values(HeaderAccept), ",")
	var representation ResponseRepresentation
	var body []byte
	var err error

	representation, err = n.selectRepresentation(accept)
	if err != nil {
		return nil, err
	}

	body, err = representation.Encode(value)
	if err != nil {
		return nil, fmt.Errorf("encode %s response: %w", representation.MediaType, err)
	}

	return NewResponse(
		WithBody(body),
		WithHeader(HeaderContentType, responseContentType(representation.MediaType)),
		WithHeader(HeaderVary, HeaderAccept),
	), nil
}

func (n *ContentNegotiator) selectRepresentation(accept string) (ResponseRepresentation, error) {
	if strings.TrimSpace(accept) == "" {
		return n.representation(n.defaultMediaType), nil
	}

	ranges, err := parseAccept(accept)
	if err != nil {
		return ResponseRepresentation{}, &NotAcceptableError{Accept: accept}
	}

	var selected ResponseRepresentation
	var selectedMatch representationMatch
	selectedFound := false
	for representationIndex, representation := range n.representations {
		match, ok := bestRepresentationMatch(representation.mediaType, ranges)
		if !ok || match.quality <= 0 {
			continue
		}
		match.representationIndex = representationIndex

		if !selectedFound || betterRepresentationMatch(match, selectedMatch) {
			selected = representation.ResponseRepresentation
			selectedMatch = match
			selectedFound = true
		}
	}

	if !selectedFound {
		return ResponseRepresentation{}, &NotAcceptableError{Accept: accept}
	}

	return selected, nil
}

func (n *ContentNegotiator) representation(mediaType string) ResponseRepresentation {
	for _, representation := range n.representations {
		if representation.mediaType == mediaType {
			return representation.ResponseRepresentation
		}
	}

	panic(fmt.Sprintf("response media type %q is not configured", mediaType))
}

// NotAcceptableError indicates that none of the configured response formats
// matches the client's Accept header.
type NotAcceptableError struct {
	Accept string
}

func (e *NotAcceptableError) Error() string {
	return fmt.Sprintf("response representation is not acceptable for Accept header %q", e.Accept)
}

func (*NotAcceptableError) StatusCode() int {
	return http.StatusNotAcceptable
}

type acceptRange struct {
	mediaType string
	quality   float64
	order     int
}

type representationMatch struct {
	quality             float64
	specificity         int
	acceptOrder         int
	representationIndex int
}

func parseAccept(value string) ([]acceptRange, error) {
	parts := strings.Split(value, ",")
	ranges := make([]acceptRange, 0, len(parts))
	var mediaType string
	var err error

	for order, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		mediaTypeValue, parameters, _ := strings.Cut(part, ";")
		mediaType, err = parseMediaRange(mediaTypeValue)
		if err != nil {
			return nil, err
		}

		quality := 1.0
		for _, parameter := range strings.Split(parameters, ";") {
			name, parameterValue, hasValue := strings.Cut(strings.TrimSpace(parameter), "=")
			if !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			if !hasValue {
				return nil, errors.New("Accept quality has no value")
			}

			quality, err = parseQuality(strings.TrimSpace(parameterValue))
			if err != nil {
				return nil, err
			}

			break
		}

		ranges = append(ranges, acceptRange{
			mediaType: mediaType,
			quality:   quality,
			order:     order,
		})
	}
	if len(ranges) == 0 {
		return nil, errors.New("Accept header contains no media ranges")
	}

	return ranges, nil
}

func bestRepresentationMatch(mediaType string, ranges []acceptRange) (representationMatch, bool) {
	selected := representationMatch{}
	found := false
	for _, acceptRange := range ranges {
		specificity, ok := mediaRangeSpecificity(acceptRange.mediaType, mediaType)
		if !ok {
			continue
		}

		match := representationMatch{
			quality:     acceptRange.quality,
			specificity: specificity,
			acceptOrder: acceptRange.order,
		}
		// A more specific matching range takes precedence over a less specific
		// range. This is important for exclusions such as */*;q=1,
		// application/json;q=0.
		if !found || match.specificity > selected.specificity ||
			(match.specificity == selected.specificity && match.acceptOrder < selected.acceptOrder) {
			selected = match
			found = true
		}
	}

	return selected, found
}

func betterRepresentationMatch(candidate representationMatch, selected representationMatch) bool {
	if candidate.quality != selected.quality {
		return candidate.quality > selected.quality
	}
	if candidate.specificity != selected.specificity {
		return candidate.specificity > selected.specificity
	}
	if candidate.acceptOrder != selected.acceptOrder {
		return candidate.acceptOrder < selected.acceptOrder
	}

	return candidate.representationIndex < selected.representationIndex
}

func mediaRangeSpecificity(mediaRange string, mediaType string) (int, bool) {
	rangeType, rangeSubtype, _ := strings.Cut(mediaRange, "/")
	candidateType, candidateSubtype, _ := strings.Cut(mediaType, "/")

	if rangeType != "*" && rangeType != candidateType {
		return 0, false
	}
	if rangeSubtype != "*" && rangeSubtype != candidateSubtype {
		return 0, false
	}

	specificity := 0
	if rangeType != "*" {
		specificity++
	}
	if rangeSubtype != "*" {
		specificity++
	}

	return specificity, true
}

func parseConfiguredMediaType(value string) (string, error) {
	var mediaType string
	var err error

	mediaType, err = parseMediaRange(value)
	if err != nil {
		return "", err
	}
	if strings.Contains(mediaType, "*") {
		return "", fmt.Errorf("wildcards are not valid response media types")
	}

	return mediaType, nil
}

func parseMediaRange(value string) (string, error) {
	var mediaType string
	var err error

	mediaType, _, err = mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	mediaType = strings.ToLower(mediaType)
	if !validMediaRange(mediaType) {
		return "", fmt.Errorf("invalid media range %q", value)
	}

	return mediaType, nil
}

func parseQuality(value string) (float64, error) {
	quality, err := strconv.ParseFloat(value, 64)
	if err != nil || !(quality >= 0 && quality <= 1) {
		return 0, fmt.Errorf("invalid Accept quality %q", value)
	}

	return quality, nil
}

func validMediaRange(mediaType string) bool {
	if mediaType == "*/*" {
		return true
	}
	if strings.Count(mediaType, "/") != 1 {
		return false
	}

	mediaTypeName, subtype, _ := strings.Cut(mediaType, "/")
	if mediaTypeName == "" || subtype == "" {
		return false
	}
	if mediaTypeName == "*" && subtype != "*" {
		return false
	}
	if strings.Contains(mediaTypeName, "*") && mediaTypeName != "*" {
		return false
	}
	if strings.Contains(subtype, "*") && subtype != "*" {
		return false
	}

	return true
}

func responseContentType(mediaType string) string {
	switch mediaType {
	case ContentTypeApplicationJson:
		return ContentTypeJson
	case ContentTypeApplicationXml:
		return ContentTypeXml
	default:
		return mediaType + "; charset=utf-8"
	}
}
