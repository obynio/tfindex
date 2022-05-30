package main

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-gonic/gin"
)

// Version contains the current version of the backend provided by ldflags
var version string

type Api struct {
	s3 *s3.S3
}

// Config is the root of the backend configuration, used as an entrypoint by kong to generate the cli
var Config struct {
	Debug   bool             `help:"Enable debug logging."`
	Version kong.VersionFlag `help:"Print version information and quit."`
}

type ErrorResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

type Platform struct {
	Os   string `json:"os"`
	Arch string `json:"arch"`
}

type Version struct {
	Version   string     `json:"version"`
	Protocols []string   `json:"protocols,omitempty"`
	Platforms []Platform `json:"platforms"`
}

type VersionResponse struct {
	ID       string      `json:"id"`
	Versions []Version   `json:"versions"`
	Warnings interface{} `json:"warnings"`
}

func (a *Api) httpGetWellKnown(c *gin.Context) {
	response := struct {
		Providers string `json:"providers.v1"`
	}{
		Providers: "/v1/providers/",
	}
	c.JSON(http.StatusOK, response)
}

func (a *Api) collectVersions(namespace, modtype string) ([]Version, error) {
	versions := make(map[string][]Platform)

	params := &s3.ListObjectsV2Input{
		Bucket: aws.String("tfindex"),
		Prefix: aws.String("algolia/restapi"),
	}

	versionDirs, err := a.s3.ListObjectsV2(params)
	if err != nil {
		fmt.Println(err)
		return []Version{}, err
	}

	for _, v := range versionDirs.Contents {
		if strings.HasSuffix(*v.Key, ".zip") {
			trunc := strings.TrimLeft(*v.Key, *params.Prefix)
			intel := strings.Split(trunc, "/")

			if len(intel) == 3 {
				version := intel[0]
				osArch := strings.Split(intel[1], "_")

				fmt.Println(version, osArch[0], osArch[1])

				versions[version] = append(versions[version], Platform{Os: osArch[0], Arch: osArch[1]})
			}
		}
	}

	reply := []Version{}

	for k, v := range versions {
		reply = append(reply, Version{Version: k, Platforms: v})
	}

	return reply, nil
}

func (a *Api) httpGetProvider(c *gin.Context) {
	namespace := c.Param("namespace")
	modtype := c.Param("type")

	versions, err := a.collectVersions(namespace, modtype)
	if err != nil {
		c.JSON(http.StatusBadRequest, &ErrorResponse{
			Status:  http.StatusBadRequest,
			Message: err.Error(),
		})
	}

	c.JSON(http.StatusOK, versions)
}

func setupRouter() *gin.Engine {
	//gin.SetMode(gin.ReleaseMode)

	// Create an AWS client session
	sessionOptions := session.Options{
		Profile:                 "edge",
		SharedConfigState:       session.SharedConfigEnable,
		AssumeRoleTokenProvider: stscreds.StdinTokenProvider,
		Config: aws.Config{
			Region: aws.String("eu-west-1"),
		},
	}

	sess, err := session.NewSessionWithOptions(sessionOptions)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	svc := s3.New(sess)

	handler := Api{
		s3: svc,
	}

	r := gin.New()
	r.Use(
		gin.Recovery(),
		gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
			return fmt.Sprintf("%v [INFO][GIN] %s \"%s %s %s %d %s \"%s\" %s\"\n",
				param.TimeStamp.Format("2006-01-02T15:04:05Z"),
				param.ClientIP,
				param.Method,
				param.Path,
				param.Request.Proto,
				param.StatusCode,
				param.Latency,
				param.Request.UserAgent(),
				param.ErrorMessage,
			)
		}),
	)

	r.GET("/.well-known/terraform.json", handler.httpGetWellKnown)
	r.GET("/v1/providers/:namespace/:type/versions", handler.httpGetProvider)

	return r
}

func main() {
	ctx := kong.Parse(&Config,
		kong.Name("tfindex"),
		kong.Description("A terraform provider registry for Algolia."),
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
			Summary: true,
		}),
		kong.Vars{"version": version + " (" + runtime.Version() + ")"})

	switch ctx.Command() {
	default:
		r := setupRouter()
		r.Run(":8080")
	}
}
