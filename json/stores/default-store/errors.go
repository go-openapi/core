package store

type storeError string

func (e storeError) Error() string {
	return string(e)
}

const ErrStore storeError = "json document store error"
