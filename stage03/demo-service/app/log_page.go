package app

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

func (a *Application) logPage(c *gin.Context) {
	c.HTML(http.StatusOK, "logs.page.tmpl", gin.H{"title": "Logs", "severity": "info", "sent": c.Query("sent")})
}

func (a *Application) sendLog(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	if err := c.Request.ParseForm(); err != nil {
		c.HTML(http.StatusBadRequest, "logs.page.tmpl", gin.H{"title": "Logs", "severity": "info", "error": "Unable to read the form. Keep the message to 4,096 characters."})
		return
	}
	message := c.Request.PostForm.Get("message")
	severity := c.Request.PostForm.Get("severity")
	levels := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	level, valid := levels[severity]
	data := gin.H{"title": "Logs", "message": message, "severity": severity}
	if !valid {
		data["error"] = "Choose a valid severity."
	} else if strings.TrimSpace(message) == "" || !utf8.ValidString(message) || utf8.RuneCountInString(message) > 4096 {
		data["error"] = "Enter a message between 1 and 4,096 characters."
	}
	if data["error"] != nil {
		c.HTML(http.StatusBadRequest, "logs.page.tmpl", data)
		return
	}
	ctx := c.Request.Context()
	if !a.logger().Enabled(ctx, level) {
		c.Redirect(http.StatusSeeOther, "/logs?sent=filtered")
		return
	}
	a.logger().LogAttrs(ctx, level, message, slog.String("log.source", "web"))
	c.Redirect(http.StatusSeeOther, "/logs?sent=written")
}
