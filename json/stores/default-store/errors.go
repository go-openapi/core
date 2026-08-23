// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

package store

type storeError string

func (e storeError) Error() string {
	return string(e)
}

const ErrStore storeError = "json document store error"
