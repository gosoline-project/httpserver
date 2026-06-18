package httpserver

import (
	"regexp"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/justtrackio/gosoline/pkg/cfg"
)

// Cors creates CORS middleware from legacy api_cors_* configuration keys.
func Cors(config cfg.Config) (gin.HandlerFunc, error) {
	var err error
	var allowedOriginPattern string
	var allowedHeaders []string
	var allowedMethods []string

	if allowedOriginPattern, err = config.GetString("api_cors_allowed_origin_pattern"); err != nil {
		return nil, err
	}

	validOrigin := regexp.MustCompile("^(?:" + allowedOriginPattern + ")$")

	if allowedHeaders, err = config.GetStringSlice("api_cors_allowed_headers"); err != nil {
		return nil, err
	}

	if allowedMethods, err = config.GetStringSlice("api_cors_allowed_methods"); err != nil {
		return nil, err
	}

	return cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			return validOrigin.MatchString(origin)
		},
		AllowHeaders:     allowedHeaders,
		AllowMethods:     allowedMethods,
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}), nil
}
