package httpserver

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/go-resty/resty/v2"
	"github.com/justtrackio/gosoline/pkg/cfg"
	"github.com/justtrackio/gosoline/pkg/kernel"
	"github.com/justtrackio/gosoline/pkg/log"
	"github.com/justtrackio/gosoline/pkg/test/env"
	"github.com/justtrackio/gosoline/pkg/test/suite"
	"github.com/stretchr/testify/assert"
)

func init() {
	suite.RegisterTestCaseDefinition("gosoline-httpserver", isTestCaseHttpserver, buildTestCaseHttpserver)
}

const expectedTestCaseHttpserverSignature = "func (s TestingSuite) TestFunc(AppUnderTest, *resty.Client) error"

type TestingSuiteHttpServerRouterAware interface {
	SetupHttpServerRouter() RouterFactory
}

func isTestCaseHttpserver(s suite.TestingSuite, method reflect.Method) error {
	if _, ok := s.(TestingSuiteHttpServerRouterAware); !ok {
		return fmt.Errorf("the suite has to implement the TestingSuiteHttpServerRouterAware interface to be able to run httpserver test cases")
	}

	if method.Func.Type().NumIn() != 3 {
		return fmt.Errorf("expected %q, but function has %d arguments", expectedTestCaseHttpserverSignature, method.Func.Type().NumIn())
	}

	if method.Func.Type().NumOut() != 1 {
		return fmt.Errorf("expected %q, but function has %d return values", expectedTestCaseHttpserverSignature, method.Func.Type().NumOut())
	}

	actualType0 := method.Func.Type().In(0)
	expectedType0 := reflect.TypeOf((*suite.TestingSuite)(nil)).Elem()

	if !actualType0.Implements(expectedType0) {
		return fmt.Errorf("expected %q, but first argument type/receiver type is %s", expectedTestCaseHttpserverSignature, actualType0.String())
	}

	actualType1 := method.Func.Type().In(1)
	expectedType1 := reflect.TypeOf((*suite.AppUnderTest)(nil)).Elem()

	if actualType1 != expectedType1 {
		return fmt.Errorf("expected %q, but first argument type is %s", expectedTestCaseHttpserverSignature, actualType1.String())
	}

	actualType2 := method.Func.Type().In(2)
	expectedType2 := reflect.TypeOf((*resty.Client)(nil))

	if actualType2 != expectedType2 {
		return fmt.Errorf("expected %q, but last argument type is %s", expectedTestCaseHttpserverSignature, actualType2.String())
	}

	actualTypeResult := method.Func.Type().Out(0)
	expectedTypeResult := reflect.TypeOf((*error)(nil)).Elem()

	if actualTypeResult != expectedTypeResult {
		return fmt.Errorf("expected %q, but return type is %s", expectedTestCaseHttpserverSignature, actualTypeResult.String())
	}

	return nil
}

func buildTestCaseHttpserver(s suite.TestingSuite, method reflect.Method) (suite.TestCaseRunner, error) {
	return runTestCaseHttpserver(s, func(s suite.TestingSuite, app suite.AppUnderTest, client *resty.Client) {
		out := method.Func.Call([]reflect.Value{
			reflect.ValueOf(s),
			reflect.ValueOf(app),
			reflect.ValueOf(client),
		})

		result := out[0].Interface()

		if result == nil {
			return
		}

		if err := result.(error); err != nil {
			assert.FailNow(s.T(), err.Error(), "testcase %s returned an unexpected error: %s", method.Name, err)
		}
	})
}

func runTestCaseHttpserver(s suite.TestingSuite, testCase func(suite suite.TestingSuite, app suite.AppUnderTest, client *resty.Client)) (suite.TestCaseRunner, error) {
	var ok bool
	var httpServerRouterAware TestingSuiteHttpServerRouterAware
	var server *HttpServer

	if httpServerRouterAware, ok = s.(TestingSuiteHttpServerRouterAware); !ok {
		return nil, fmt.Errorf("the suite has to implement the TestingSuiteHttpServerRouterAware interface to be able to run httpserver test cases")
	}

	return func(t *testing.T, s suite.TestingSuite, suiteConf *suite.SuiteConfiguration, environment *env.Environment) {
		// we first have to set up t, otherwise the test suite can't assert that there are no errors when setting up
		// route definitions or test cases
		s.SetT(t)

		routerFactory := httpServerRouterAware.SetupHttpServerRouter()

		extraOptions := []suite.Option{
			suite.WithModule("api", func(ctx context.Context, config cfg.Config, logger log.Logger) (kernel.Module, error) {
				module, err := NewServer("default", routerFactory)(ctx, config, logger)
				if err != nil {
					return nil, fmt.Errorf("failed to create test http server: %w", err)
				}

				server = module.(*HttpServer)

				return server, nil
			}),
			suite.WithConfigMap(map[string]any{
				"httpserver": map[string]any{
					"default": map[string]any{
						"port": 0,
					},
				},
			}),
		}

		suite.RunTestCaseApplication(t, s, suiteConf, environment, func(app suite.AppUnderTest) {
			port, err := server.GetPort()
			if err != nil {
				assert.FailNow(t, err.Error(), "can not get port of server")

				return
			}

			url := fmt.Sprintf("http://127.0.0.1:%d", *port)
			client := resty.New().SetBaseURL(url)

			testCase(s, app, client)
		}, extraOptions...)
	}, nil
}
