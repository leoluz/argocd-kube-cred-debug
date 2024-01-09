package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthv1beta1 "k8s.io/client-go/pkg/apis/clientauthentication/v1beta1"
)

const (
	clusterIDHeader = "x-k8s-aws-id"
	// The sts GetCallerIdentity request is valid for 15 minutes regardless of this parameters value after it has been
	// signed, but we set this unused parameter to 60 for legacy reasons (we check for a value between 0 and 60 on the
	// server side in 0.3.0 or earlier).  IT IS IGNORED.  If we can get STS to support x-amz-expires, then we should
	// set this parameter to the actual expiration, and make it configurable.
	requestPresignParam = 60
	// The actual token expiration (presigned STS urls are valid for 15 minutes after timestamp in x-amz-date).
	presignedURLExpiration       = 15 * time.Minute
	v1Prefix                     = "k8s-aws-v1."
	AWSProviderNotExpirerErrCode = "ProviderNotExpirer"
)

// newAWSCommand returns a new instance of an aws command that generates k8s auth token
// implementation is "inspired" by https://github.com/kubernetes-sigs/aws-iam-authenticator/blob/e61f537662b64092ed83cb76e600e023f627f628/pkg/token/token.go#L316
func main() {
	clusterName := flag.String("cluster-name", "", "")
	roleARN := flag.String("role-arn", "", "")
	flag.Parse()

	if clusterName == nil || *clusterName == "" {
		exitIfErr("", fmt.Errorf("cluster-name not provided"), 25)
	}

	presignedURLString, sessionExpiresAt, err := getSignedRequestWithRetry(context.TODO(), time.Minute, 5*time.Second, *clusterName, *roleARN, getSignedRequest)
	if err != nil {

		if strings.Contains(err.Error(), "ProviderNotExpirer") {
			exitIfErr("error get expiration", err, 14)

		}
		exitIfErr("error signing request with retry", err, 13)
	}
	if presignedURLString == "" {
		os.Exit(55)
	}
	token := v1Prefix + base64.RawURLEncoding.EncodeToString([]byte(presignedURLString))
	tokenExpiration, err := getTokenExpirationDate(sessionExpiresAt)
	exitIfErr("error getting token expiration date", err, 3)

	result := formatJSON(token, tokenExpiration)
	_, err = fmt.Fprint(os.Stdout, result)
	exitIfErr("error printing to stdout", err, 3)
}

func exitIfErr(msg string, err error, code int) {
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("%s: %s", msg, err))
		fmt.Fprintln(os.Stdout, fmt.Errorf("%s: %s", msg, err))
		if code == 0 {
			code = 100
		}
		os.Exit(code)
	}
}

type getSignedRequestFunc func(clusterName, roleARN string) (string, *time.Time, error)

// getTokenExpirationDate will compare the given sessionExpiresAt with the predefined
// STS token expiration of 15 minutes. It will return the more recent time minus 1 minute
// for some cushion. Will return error if sessionExpiresAt is < 1 minute.
// This will avoid the error "the server has asked for the client to provide credentials"
// in Argo CD.
func getTokenExpirationDate(sessionExpiresAt *time.Time) (time.Time, error) {
	tokenExpiration := time.Now().Local().Add(presignedURLExpiration - 1*time.Minute)
	if sessionExpiresAt == nil {
		return tokenExpiration, nil
	}
	sessionExpiresAtSafe := sessionExpiresAt.Add(-time.Minute)
	if sessionExpiresAtSafe.Before(time.Now()) {
		return time.Time{}, fmt.Errorf("session expires in less than one minute")
	}
	if sessionExpiresAtSafe.Before(tokenExpiration) {
		return sessionExpiresAtSafe, nil
	}
	return tokenExpiration, nil
}

func getSignedRequestWithRetry(ctx context.Context, timeout, interval time.Duration, clusterName, roleARN string, fn getSignedRequestFunc) (string, *time.Time, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		signed, sessionExpiresAt, err := fn(clusterName, roleARN)
		if err == nil {
			return signed, sessionExpiresAt, nil
		}
		select {
		case <-ctx.Done():
			return "", nil, fmt.Errorf("timeout while trying to get signed aws request: last error: %s", err)
		case <-time.After(interval):
		}
	}
}

func getSignedRequest(clusterName, roleARN string) (string, *time.Time, error) {
	sess, err := session.NewSession()
	if err != nil {
		return "", nil, fmt.Errorf("error creating new AWS session: %s", err)
	}
	stsAPI := sts.New(sess)
	if roleARN != "" {
		creds := stscreds.NewCredentials(sess, roleARN)
		stsAPI = sts.New(sess, &aws.Config{Credentials: creds})
	}
	request, _ := stsAPI.GetCallerIdentityRequest(&sts.GetCallerIdentityInput{})
	request.HTTPRequest.Header.Add(clusterIDHeader, clusterName)
	signed, err := request.Presign(requestPresignParam)
	if err != nil {
		return "", nil, fmt.Errorf("error presigning AWS request: %s", err)
	}
	sessionExpiresAt, err := sess.Config.Credentials.ExpiresAt()
	if err != nil {
		// some credentials providers don't support the aws.Expirer interface and
		// an error will be returned in this case.
		if awsErr, ok := err.(awserr.Error); ok {
			// if the credentials provider can't answer what the token expiration is
			// we ignore it. This might lead to the "server has asked the client to
			// provide credentials" but there is no way to know if the session will
			// be expired during the lifetime of the signed URL.
			if awsErr.Code() == AWSProviderNotExpirerErrCode {
				return signed, nil, nil
			}
		}
		return "", nil, fmt.Errorf("error getting AWS session expiration time: %s", err)
	}

	return signed, &sessionExpiresAt, nil
}

func formatJSON(token string, expiration time.Time) string {
	expirationTimestamp := metav1.NewTime(expiration)
	execInput := &clientauthv1beta1.ExecCredential{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "client.authentication.k8s.io/v1beta1",
			Kind:       "ExecCredential",
		},
		Status: &clientauthv1beta1.ExecCredentialStatus{
			ExpirationTimestamp: &expirationTimestamp,
			Token:               token,
		},
	}
	enc, _ := json.Marshal(execInput)
	return string(enc)
}
