package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/discordia-grupo01/admin/internal/changes"
)

var changesOfRoute = map[string][]string{
	"POST /v1/admin/users/:id/suspension":            {changes.Users, changes.Dossier},
	"DELETE /v1/admin/users/:id/suspension":          {changes.Users, changes.Dossier},
	"DELETE /v1/admin/servers/:id":                   {changes.Servers, changes.Dossier},
	"POST /v1/admin/servers/:id/suspension":          {changes.Servers, changes.Dossier},
	"DELETE /v1/admin/servers/:id/suspension":        {changes.Servers, changes.Dossier},
	"POST /v1/admin/recovery-requests/:id/approval":  {changes.RecoveryRequests, changes.Dossier},
	"POST /v1/admin/recovery-requests/:id/rejection": {changes.RecoveryRequests, changes.Dossier},
}

func NotifyChanges(notifier changes.Notifier) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		status := c.Writer.Status()
		if status < 200 || status >= 300 {
			return
		}
		if resources, ok := changesOfRoute[c.Request.Method+" "+c.FullPath()]; ok {
			notifier.Changed(c.Request.Context(), resources...)
		}
	}
}
