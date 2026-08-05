package esim

import "errors"

// DeleteProfileErrorCode 表示 DeleteProfile 场景的可判别错误类别。
type DeleteProfileErrorCode string

const (
	DeleteProfileErrorInvalidICCID    DeleteProfileErrorCode = "INVALID_ICCID"
	DeleteProfileErrorInvalidAIDHex   DeleteProfileErrorCode = "INVALID_AID_HEX"
	DeleteProfileErrorProfileNotFound DeleteProfileErrorCode = "PROFILE_NOT_FOUND"
	DeleteProfileErrorEUICCNotFound   DeleteProfileErrorCode = "EUICC_NOT_FOUND"
	DeleteProfileErrorBusy            DeleteProfileErrorCode = "BUSY"
	DeleteProfileErrorInternal        DeleteProfileErrorCode = "INTERNAL"
)

// DeleteProfileError 为删除 profile 提供结构化错误，便于 API 做稳定状态码映射。
type DeleteProfileError struct {
	Code    DeleteProfileErrorCode
	Message string
	Err     error
}

func (e *DeleteProfileError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "delete profile failed"
}

func (e *DeleteProfileError) Unwrap() error { return e.Err }

// NewDeleteProfileError 构造一个可判别的 DeleteProfileError。
func NewDeleteProfileError(code DeleteProfileErrorCode, message string, err error) error {
	return &DeleteProfileError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// ClassifyDeleteProfileError 返回 DeleteProfile 错误类别。
func ClassifyDeleteProfileError(err error) DeleteProfileErrorCode {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrOperationInProgress) {
		return DeleteProfileErrorBusy
	}
	var de *DeleteProfileError
	if errors.As(err, &de) && de.Code != "" {
		return de.Code
	}
	return DeleteProfileErrorInternal
}

func IsDeleteProfileInvalidInput(err error) bool {
	switch ClassifyDeleteProfileError(err) {
	case DeleteProfileErrorInvalidICCID, DeleteProfileErrorInvalidAIDHex:
		return true
	default:
		return false
	}
}

func IsDeleteProfileNotFound(err error) bool {
	switch ClassifyDeleteProfileError(err) {
	case DeleteProfileErrorProfileNotFound, DeleteProfileErrorEUICCNotFound:
		return true
	default:
		return false
	}
}

func IsDeleteProfileBusy(err error) bool {
	return ClassifyDeleteProfileError(err) == DeleteProfileErrorBusy
}
