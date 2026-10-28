package service

import (
	_ "example.com/bad/internal/modules/a/store"
	_ "example.com/bad/internal/modules/b/contract"
	_ "example.com/bad/internal/modules/b/store"
	_ "example.com/bad/internal/platform/x"
)
