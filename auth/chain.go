package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NewChainHandler(authenticators map[string]Authenticator) gin.HandlerFunc {
	return func(ginCtx *gin.Context) {
		var err error
		var valid bool

		errors := make(map[string]string)

		for n, a := range authenticators {
			if valid, err = a.IsValid(ginCtx); err != nil {
				errors[n] = err.Error()

				continue
			}

			if valid {
				return
			}
		}

		ginCtx.JSON(http.StatusUnauthorized, errors)
		ginCtx.Abort()
	}
}
