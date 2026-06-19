package httpserver

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

type CorsSettings struct {
	AllowedOriginPattern string   `cfg:"allowed_origin_pattern"`
	AllowedHeaders       []string `cfg:"allowed_headers"`
	AllowedMethods       []string `cfg:"allowed_methods"`
}

func CorsFactory(_ context.Context, config cfg.Config, _ log.Logger, settings *Settings) (gin.HandlerFunc, error) {
	return Cors(config, settings.Name)
}

// Cors creates CORS middleware from legacy api_cors_* configuration keys.
func Cors(config cfg.Config, name string) (gin.HandlerFunc, error) {
	var err error

	key := fmt.Sprintf("httpserver.%s.cors", name)
	settings := &CorsSettings{}
	if err = config.UnmarshalKey(key, settings); err != nil {
		return nil, fmt.Errorf("error unmarshalling cors settings: %w", err)
	}

	validOrigin := regexp.MustCompile("^(?:" + settings.AllowedOriginPattern + ")$")

	return cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			return validOrigin.MatchString(origin)
		},
		AllowHeaders:     settings.AllowedHeaders,
		AllowMethods:     settings.AllowedMethods,
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}), nil
}
