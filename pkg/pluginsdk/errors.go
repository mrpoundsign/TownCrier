package pluginsdk

import (
	"errors"
	"fmt"

	"github.com/mrpoundsign/TownCrier/pkg/pluginv1"
)

// APIError is a structured, user-readable error returned across the plugin boundary.
type APIError struct {
	Code              pluginv1.ErrorCode
	Message           string
	Detail            string
	RequiredMajor     uint32
	RequiredMinor     uint32
	MissingCapability string
}

// Error implements the error interface with actionable version/capability details when present.
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	msg := e.Message
	if msg == "" {
		msg = e.Code.String()
	}
	if e.Detail != "" {
		return fmt.Sprintf("%s (%s)", msg, e.Detail)
	}
	return msg
}

// FromProto converts a protobuf APIError into a Go error (returning nil if pb == nil or code == UNSPECIFIED and message == "").
func FromProto(pb *pluginv1.APIError) error {
	if pb == nil {
		return nil
	}
	if pb.GetCode() == pluginv1.ErrorCode_ERROR_CODE_UNSPECIFIED && pb.GetMessage() == "" && pb.GetDetail() == "" {
		return nil
	}
	return &APIError{
		Code:              pb.GetCode(),
		Message:           pb.GetMessage(),
		Detail:            pb.GetDetail(),
		RequiredMajor:     pb.GetRequiredMajor(),
		RequiredMinor:     pb.GetRequiredMinor(),
		MissingCapability: pb.GetMissingCapability(),
	}
}

// ToProto converts a Go error into a protobuf APIError.
func ToProto(err error) *pluginv1.APIError {
	if err == nil {
		return nil
	}
	if apiErr, ok := errors.AsType[*APIError](err); ok {
		return &pluginv1.APIError{
			Code:              apiErr.Code,
			Message:           apiErr.Message,
			Detail:            apiErr.Detail,
			RequiredMajor:     apiErr.RequiredMajor,
			RequiredMinor:     apiErr.RequiredMinor,
			MissingCapability: apiErr.MissingCapability,
		}
	}
	return &pluginv1.APIError{
		Code:    pluginv1.ErrorCode_ERROR_CODE_INTERNAL,
		Message: err.Error(),
	}
}
