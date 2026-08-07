package main

import (
	"io"
	"os"

	"github.com/cockroachdb/errors"
)

func copyFile(src, dst string) error {
	dstTmp := dst + ".tmp"
	if err := os.Remove(dstTmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.Wrapf(err, "main.copyFile remove stale destination %s", errors.Safe(dstTmp))
	}
	if err := cp(src, dstTmp); err != nil {
		return err
	}
	if err := os.Rename(dstTmp, dst); err != nil {
		return errors.Wrapf(err, "main.copyFile rename destination %s", errors.Safe(dst))
	}
	return nil
}

func cp(src, dst string) (err error) {
	sourceFileInfo, err := os.Stat(src)
	if err != nil {
		return errors.Wrapf(err, "main.cp stat source %s", errors.Safe(src))
	}
	if !sourceFileInfo.Mode().IsRegular() {
		return errors.Errorf("main.cp source %s is not a regular file", errors.Safe(src))
	}

	source, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "main.cp open source %s", errors.Safe(src))
	}
	defer func() {
		err = errors.CombineErrors(err, errors.Wrap(source.Close(), "main.cp close source"))
	}()

	destination, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceFileInfo.Mode().Perm())
	if err != nil {
		return errors.Wrapf(err, "main.cp create destination %s", errors.Safe(dst))
	}
	defer func() {
		err = errors.CombineErrors(err, errors.Wrap(destination.Close(), "main.cp close destination"))
	}()

	if _, err := io.Copy(destination, source); err != nil {
		return errors.Wrap(err, "main.cp copy")
	}
	return nil
}
