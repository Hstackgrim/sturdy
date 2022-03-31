package db

import "getsturdy.com/api/pkg/di"

func Module(c *di.Container) {
	c.Register(NewActivityReadsRepository)
	c.Register(NewActivityRepository)
}
