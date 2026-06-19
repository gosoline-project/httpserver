package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gosoline-project/httpserver"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/log"
)

const ByJWT = "jwtAuth"

type jwtAuthenticator struct {
	jwtTokenHandler JwtTokenHandler
}

func JwtAuthHandlerFactory(ctx context.Context, config cfg.Config, logger log.Logger, settings *httpserver.Settings) (gin.HandlerFunc, error) {
	return NewJwtAuthHandler(config, settings.Name)
}

func NewJwtAuthHandler(config cfg.Config, name string) (gin.HandlerFunc, error) {
	var err error
	var auth Authenticator

	if auth, err = NewJWTAuthAuthenticator(config, name); err != nil {
		return nil, fmt.Errorf("can not create jwt authenticator for %s: %w", name, err)
	}

	return func(ginCtx *gin.Context) {
		valid, err := auth.IsValid(ginCtx)

		if valid {
			return
		}

		if err == nil {
			err = fmt.Errorf("the user jwt token isn't valid nor was there an error")
		}

		ginCtx.JSON(http.StatusUnauthorized, gin.H{"err": err.Error()})
		ginCtx.Abort()
	}, nil
}

func NewJWTAuthAuthenticator(config cfg.Config, name string) (Authenticator, error) {
	var err error
	var jwtTokenHandler JwtTokenHandler

	if jwtTokenHandler, err = NewJwtTokenHandler(config, name); err != nil {
		return nil, fmt.Errorf("can not create jwt token handler for authenticator %s: %w", name, err)
	}

	return NewJWTAuthAuthenticatorWithInterfaces(jwtTokenHandler), nil
}

func NewJWTAuthAuthenticatorWithInterfaces(jwtTokenHandler JwtTokenHandler) Authenticator {
	return &jwtAuthenticator{
		jwtTokenHandler: jwtTokenHandler,
	}
}

func (a *jwtAuthenticator) IsValid(ginCtx *gin.Context) (bool, error) {
	var err error
	var isValid bool
	var token *jwt.Token

	bearerAuth := ginCtx.GetHeader(httpserver.HeaderAuthorization)

	if bearerAuth == "" {
		return false, fmt.Errorf("no credentials provided")
	}

	if !strings.HasPrefix(bearerAuth, "Bearer ") {
		return false, fmt.Errorf("could not find jwt token in header")
	}

	jwtToken := bearerAuth[len("Bearer "):]

	if isValid, token, err = a.jwtTokenHandler.Valid(jwtToken); err != nil {
		return false, fmt.Errorf("error while validating jwt token: %w", err)
	}

	if !isValid {
		return false, fmt.Errorf("invalid jwt token provided")
	}

	email, ok := token.Claims.(jwt.MapClaims)["email"].(string)
	if !ok || email == "" {
		return false, fmt.Errorf("jwt token is missing email field")
	}

	subject := &Subject{
		Name:            email,
		Anonymous:       false,
		AuthenticatedBy: ByJWT,
	}
	RequestWithSubject(ginCtx, subject)

	return true, nil
}
