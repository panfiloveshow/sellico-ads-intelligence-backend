package envelope

// Response is the unified API response wrapper (Response_Envelope).
type Response struct {
	Data   interface{} `json:"data"`
	Meta   *Meta       `json:"meta,omitempty"`
	Errors []Error     `json:"errors,omitempty"`
}

// Meta contains pagination metadata.
type Meta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
}

// Error represents a single error in the response.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

// OK creates a successful response with data and optional meta.
func OK(data interface{}, meta *Meta) Response {
	return Response{
		Data: data,
		Meta: meta,
	}
}

// Err creates an error response.
func Err(errors ...Error) Response {
	return Response{
		Errors: errors,
	}
}

// ValidationErr creates a validation error response from field errors.
func ValidationErr(fieldErrors map[string]string) Response {
	errs := make([]Error, 0, len(fieldErrors))
	for field, msg := range fieldErrors {
		errs = append(errs, Error{
			Code:    "VALIDATION_ERROR",
			Message: msg,
			Field:   field,
		})
	}
	return Response{
		Errors: errs,
	}
}
