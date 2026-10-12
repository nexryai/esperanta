//go:build !linux

package landlock

import "errors"

func (p *Policy) restrict() error {
	return errors.New("Landlock requires Linux; refusing to run without a sandbox")
}
