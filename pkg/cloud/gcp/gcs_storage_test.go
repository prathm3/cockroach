// Copyright 2020 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package gcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/cockroachdb/cockroach/pkg/base"
	"github.com/cockroachdb/cockroach/pkg/cloud"
	"github.com/cockroachdb/cockroach/pkg/cloud/cloudtestutils"
	"github.com/cockroachdb/cockroach/pkg/security/username"
	"github.com/cockroachdb/cockroach/pkg/settings/cluster"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/testutils/skip"
	"github.com/cockroachdb/cockroach/pkg/util/ioctx"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2/google"
)

func TestPutGoogleCloud(t *testing.T) {
	defer leaktest.AfterTest(t)()

	bucket := os.Getenv("GOOGLE_BUCKET")
	if bucket == "" {
		skip.IgnoreLint(t, "GOOGLE_BUCKET env var must be set")
	}

	user := username.RootUserName()
	testSettings := cluster.MakeTestingClusterSettings()

	testutils.RunTrueAndFalse(t, "auth-specified-with-auth-param", func(t *testing.T, specified bool) {
		credentials := os.Getenv("GOOGLE_CREDENTIALS_JSON")
		if credentials == "" {
			skip.IgnoreLint(t, "GOOGLE_CREDENTIALS_JSON env var must be set")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(credentials))
		uri := fmt.Sprintf("gs://%s/%s?%s=%s",
			bucket,
			"backup-test-specified",
			CredentialsParam,
			url.QueryEscape(encoded),
		)
		if specified {
			uri += fmt.Sprintf("&%s=%s", cloud.AuthParam, cloud.AuthParamSpecified)
		}
		cloudtestutils.CheckExportStore(
			t,
			uri,
			false,
			user,
			nil, /* db */
			testSettings,
		)
		cloudtestutils.CheckListFiles(t, fmt.Sprintf("gs://%s/%s/%s?%s=%s&%s=%s",
			bucket,
			"backup-test-specified",
			"listing-test",
			cloud.AuthParam,
			cloud.AuthParamSpecified,
			CredentialsParam,
			url.QueryEscape(encoded),
		), username.RootUserName(),
			nil, /* db */
			testSettings,
		)
	})
	t.Run("auth-implicit", func(t *testing.T) {
		if !cloudtestutils.IsImplicitAuthConfigured() {
			skip.IgnoreLint(t, "implicit auth is not configured")
		}

		cloudtestutils.CheckExportStore(
			t,
			fmt.Sprintf("gs://%s/%s?%s=%s", bucket, "backup-test-implicit",
				cloud.AuthParam, cloud.AuthParamImplicit),
			false,
			user,
			nil, /* db */
			testSettings)
		cloudtestutils.CheckListFiles(t, fmt.Sprintf("gs://%s/%s/%s?%s=%s",
			bucket,
			"backup-test-implicit",
			"listing-test",
			cloud.AuthParam,
			cloud.AuthParamImplicit,
		), username.RootUserName(),
			nil, /* db */
			testSettings,
		)
	})

	t.Run("auth-specified-bearer-token", func(t *testing.T) {
		credentials := os.Getenv("GOOGLE_CREDENTIALS_JSON")
		if credentials == "" {
			skip.IgnoreLint(t, "GOOGLE_CREDENTIALS_JSON env var must be set")
		}

		ctx := context.Background()
		source, err := google.JWTConfigFromJSON([]byte(credentials), gcs.ScopeReadWrite)
		require.NoError(t, err, "creating GCS oauth token source from specified credentials")
		ts := source.TokenSource(ctx)

		token, err := ts.Token()
		require.NoError(t, err, "getting token")

		uri := fmt.Sprintf("gs://%s/%s?%s=%s",
			bucket,
			"backup-test-specified",
			BearerTokenParam,
			token.AccessToken,
		)
		uri += fmt.Sprintf("&%s=%s", cloud.AuthParam, cloud.AuthParamSpecified)
		cloudtestutils.CheckExportStore(
			t,
			uri,
			false,
			user,
			nil, /* db */
			testSettings)
		cloudtestutils.CheckListFiles(t, fmt.Sprintf("gs://%s/%s/%s?%s=%s&%s=%s",
			bucket,
			"backup-test-specified",
			"listing-test",
			cloud.AuthParam,
			cloud.AuthParamSpecified,
			BearerTokenParam,
			token.AccessToken,
		), username.RootUserName(),
			nil, /* db */
			testSettings,
		)
	})
}

func TestGCSAssumeRole(t *testing.T) {
	user := username.RootUserName()
	testSettings := cluster.MakeTestingClusterSettings()

	limitedBucket := os.Getenv("GOOGLE_LIMITED_BUCKET")
	if limitedBucket == "" {
		skip.IgnoreLint(t, "GOOGLE_LIMITED_BUCKET env var must be set")
	}
	assumedAccount := os.Getenv("ASSUME_SERVICE_ACCOUNT")
	if assumedAccount == "" {
		skip.IgnoreLint(t, "ASSUME_SERVICE_ACCOUNT env var must be set")
	}

	t.Run("specified", func(t *testing.T) {
		credentials := os.Getenv("GOOGLE_CREDENTIALS_JSON")
		if credentials == "" {
			skip.IgnoreLint(t, "GOOGLE_CREDENTIALS_JSON env var must be set")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(credentials))

		// Verify that specified permissions with the credentials do not give us
		// access to the bucket.
		cloudtestutils.CheckNoPermission(t, fmt.Sprintf("gs://%s/%s?%s=%s", limitedBucket, "backup-test-assume-role",
			CredentialsParam, url.QueryEscape(encoded)), user,
			nil, /* db */
			testSettings,
		)

		cloudtestutils.CheckExportStore(
			t,
			fmt.Sprintf("gs://%s/%s?%s=%s&%s=%s&%s=%s",
				limitedBucket,
				"backup-test-assume-role",
				cloud.AuthParam,
				cloud.AuthParamSpecified,
				AssumeRoleParam,
				assumedAccount, CredentialsParam,
				url.QueryEscape(encoded),
			), false, user,
			nil, /* db */
			testSettings,
		)
		cloudtestutils.CheckListFiles(t, fmt.Sprintf("gs://%s/%s/%s?%s=%s&%s=%s&%s=%s",
			limitedBucket,
			"backup-test-assume-role",
			"listing-test",
			cloud.AuthParam,
			cloud.AuthParamSpecified,
			AssumeRoleParam,
			assumedAccount,
			CredentialsParam,
			url.QueryEscape(encoded),
		), username.RootUserName(),
			nil, /* db */
			testSettings,
		)
	})

	t.Run("implicit", func(t *testing.T) {
		if _, err := google.FindDefaultCredentials(context.Background()); err != nil {
			skip.IgnoreLint(t, err)
		}

		// Verify that implicit permissions with the credentials do not give us
		// access to the bucket.
		cloudtestutils.CheckNoPermission(t, fmt.Sprintf("gs://%s/%s?%s=%s", limitedBucket, "backup-test-assume-role",
			cloud.AuthParam, cloud.AuthParamImplicit), user,
			nil, /* db */
			testSettings,
		)

		cloudtestutils.CheckExportStore(t, fmt.Sprintf("gs://%s/%s?%s=%s&%s=%s", limitedBucket, "backup-test-assume-role",
			cloud.AuthParam, cloud.AuthParamImplicit, AssumeRoleParam, assumedAccount), false, user,
			nil, /* db */
			testSettings,
		)
		cloudtestutils.CheckListFiles(t, fmt.Sprintf("gs://%s/%s/%s?%s=%s&%s=%s",
			limitedBucket,
			"backup-test-assume-role",
			"listing-test",
			cloud.AuthParam,
			cloud.AuthParamImplicit,
			AssumeRoleParam,
			assumedAccount,
		), username.RootUserName(),
			nil, /* db */
			testSettings,
		)
	})

	t.Run("role-chaining", func(t *testing.T) {
		credentials := os.Getenv("GOOGLE_CREDENTIALS_JSON")
		if credentials == "" {
			skip.IgnoreLint(t, "GOOGLE_CREDENTIALS_JSON env var must be set")
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(credentials))

		roleChainStr := os.Getenv("ASSUME_SERVICE_ACCOUNT_CHAIN")
		if roleChainStr == "" {
			skip.IgnoreLint(t, "ASSUME_SERVICE_ACCOUNT_CHAIN env var must be set")
		}

		roleChain := strings.Split(roleChainStr, ",")

		for _, tc := range []struct {
			auth        string
			credentials string
		}{
			{cloud.AuthParamSpecified, encoded},
			{cloud.AuthParamImplicit, ""},
		} {
			t.Run(tc.auth, func(t *testing.T) {
				q := make(url.Values)
				q.Set(cloud.AuthParam, tc.auth)
				q.Set(CredentialsParam, tc.credentials)

				// First verify that none of the individual roles in the chain can be used
				// to access the storage.
				for _, role := range roleChain {
					q.Set(AssumeRoleParam, role)
					roleURI := fmt.Sprintf("gs://%s/%s/%s?%s",
						limitedBucket,
						"backup-test-assume-role",
						"listing-test",
						q.Encode(),
					)
					cloudtestutils.CheckNoPermission(t, roleURI, user,
						nil, /* db */
						testSettings,
					)
				}

				// Finally, check that the chain of roles can be used to access the storage.
				q.Set(AssumeRoleParam, roleChainStr)
				uri := fmt.Sprintf("gs://%s/%s/%s?%s",
					limitedBucket,
					"backup-test-assume-role",
					"listing-test",
					q.Encode(),
				)
				cloudtestutils.CheckExportStore(t, uri, false, user,
					nil, /* db */
					testSettings,
				)
				cloudtestutils.CheckListFiles(t, uri, user,
					nil, /* db */
					testSettings,
				)
			})
		}
	})
}

func TestAntagonisticGCSRead(t *testing.T) {
	defer leaktest.AfterTest(t)()

	if !cloudtestutils.IsImplicitAuthConfigured() {
		skip.IgnoreLint(t, "implicit auth is not configured")
	}

	testSettings := cluster.MakeTestingClusterSettings()

	gsFile := "gs://nightly-cloud-unit-tests/antagonistic-read?AUTH=implicit"
	conf, err := cloud.ExternalStorageConfFromURI(gsFile, username.RootUserName())
	require.NoError(t, err)

	cloudtestutils.CheckAntagonisticRead(t, conf, testSettings)
}

// TestFileDoesNotExist ensures that the ReadFile method of google cloud storage
// returns a sentinel error when the `Bucket` or `Object` being read do not
// exist.
func TestFileDoesNotExist(t *testing.T) {
	defer leaktest.AfterTest(t)()

	if !cloudtestutils.IsImplicitAuthConfigured() {
		skip.IgnoreLint(t, "implicit auth is not configured")
	}

	user := username.RootUserName()

	testSettings := cluster.MakeTestingClusterSettings()

	{
		// Invalid gsFile.
		gsFile := "gs://cockroach-fixtures/tpch-csv/sf-1/invalid_region.tbl?AUTH=implicit"
		conf, err := cloud.ExternalStorageConfFromURI(gsFile, user)
		require.NoError(t, err)

		s, err := cloud.MakeExternalStorage(context.Background(), conf, base.ExternalIODirConfig{}, testSettings,
			nil, /* blobClientFactory */
			nil, /* db */
			nil, /* limiters */
			cloud.NilMetrics,
		)
		require.NoError(t, err)
		_, _, err = s.ReadFile(context.Background(), "", cloud.ReadOptions{NoFileSize: true})
		require.Error(t, err, "")
		require.True(t, errors.Is(err, cloud.ErrFileDoesNotExist))
	}

	{
		// Invalid gsBucket.
		gsFile := "gs://cockroach-fixtures-invalid/tpch-csv/sf-1/region.tbl?AUTH=implicit"
		conf, err := cloud.ExternalStorageConfFromURI(gsFile, user)
		require.NoError(t, err)

		s, err := cloud.MakeExternalStorage(context.Background(), conf, base.ExternalIODirConfig{}, testSettings,
			nil, /* blobClientFactory */
			nil, /* db */
			nil, /* limiters */
			cloud.NilMetrics,
		)
		require.NoError(t, err)
		_, _, err = s.ReadFile(context.Background(), "", cloud.ReadOptions{NoFileSize: true})
		require.Error(t, err, "")
		require.True(t, errors.Is(err, cloud.ErrFileDoesNotExist))
	}
}

func TestCompressedGCS(t *testing.T) {
	defer leaktest.AfterTest(t)()

	if !cloudtestutils.IsImplicitAuthConfigured() {
		skip.IgnoreLint(t, "implicit auth is not configured")
	}

	user := username.RootUserName()
	ctx := context.Background()

	testSettings := cluster.MakeTestingClusterSettings()

	// gsutil cp /usr/share/dict/words gs://cockroach-fixtures/words-compressed.txt
	gsFile1 := "gs://cockroach-fixtures/words.txt?AUTH=implicit"

	// gsutil cp -Z /usr/share/dict/words gs://cockroach-fixtures/words-compressed.txt
	gsFile2 := "gs://cockroach-fixtures/words-compressed.txt?AUTH=implicit"

	conf1, err := cloud.ExternalStorageConfFromURI(gsFile1, user)
	require.NoError(t, err)
	conf2, err := cloud.ExternalStorageConfFromURI(gsFile2, user)
	require.NoError(t, err)

	s1, err := cloud.MakeExternalStorage(ctx, conf1, base.ExternalIODirConfig{}, testSettings,
		nil, /* blobClientFactory */
		nil, /* db */
		nil, /* limiters */
		cloud.NilMetrics,
	)
	require.NoError(t, err)
	s2, err := cloud.MakeExternalStorage(ctx, conf2, base.ExternalIODirConfig{}, testSettings,
		nil, /* blobClientFactory */
		nil, /* db */
		nil, /* limiters */
		cloud.NilMetrics,
	)
	require.NoError(t, err)

	reader1, _, err := s1.ReadFile(context.Background(), "", cloud.ReadOptions{NoFileSize: true})
	require.NoError(t, err)
	reader2, _, err := s2.ReadFile(context.Background(), "", cloud.ReadOptions{NoFileSize: true})
	require.NoError(t, err)

	content1, err := ioctx.ReadAll(ctx, reader1)
	require.NoError(t, err)
	require.NoError(t, reader1.Close(context.Background()))
	content2, err := ioctx.ReadAll(ctx, reader2)
	require.NoError(t, err)
	require.NoError(t, reader2.Close(context.Background()))

	require.Equal(t, string(content1), string(content2))

	// Test reading parts of the uncompressed object.
	for i := 0; i < 10; i++ {
		ofs := rand.Intn(len(content1) - 1)
		l := rand.Intn(len(content1) - ofs)
		reader, _, err := s1.ReadFile(context.Background(), "", cloud.ReadOptions{
			Offset:     int64(ofs),
			LengthHint: int64(l),
			NoFileSize: true,
		})
		require.NoError(t, err)
		content, err := ioctx.ReadAll(ctx, reader)
		require.NoError(t, err)
		require.NoError(t, reader.Close(context.Background()))
		require.Equal(t, len(content), l)
		require.Equal(t, string(content), string(content1[ofs:ofs+l]))
	}
}

// TestReadFileAtReturnsSize tests that ReadFileAt returns
// a cloud.ResumingReader that contains the size of the file.
func TestReadFileAtReturnsSize(t *testing.T) {
	defer leaktest.AfterTest(t)()

	if !cloudtestutils.IsImplicitAuthConfigured() {
		skip.IgnoreLint(t, "implicit auth is not configured")
	}

	bucket := os.Getenv("GOOGLE_BUCKET")
	if bucket == "" {
		skip.IgnoreLint(t, "GOOGLE_BUCKET env var must be set")
	}

	user := username.RootUserName()
	ctx := context.Background()
	testSettings := cluster.MakeTestingClusterSettings()
	file := "testfile"
	data := []byte("hello world")

	gsURI := fmt.Sprintf("gs://%s/%s?AUTH=implicit", bucket, "read-file-at-returns-size")
	conf, err := cloud.ExternalStorageConfFromURI(gsURI, user)
	require.NoError(t, err)
	args := cloud.ExternalStorageContext{
		IOConf:          base.ExternalIODirConfig{},
		Settings:        testSettings,
		DB:              nil,
		Options:         nil,
		Limiters:        nil,
		MetricsRecorder: cloud.NilMetrics,
	}
	s, err := makeGCSStorage(ctx, args, conf)
	require.NoError(t, err)

	w, err := s.Writer(ctx, file)
	require.NoError(t, err)

	_, err = w.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	reader, _, err := s.ReadFile(ctx, file, cloud.ReadOptions{})
	require.NoError(t, err)

	rr, ok := reader.(*cloud.ResumingReader)
	require.True(t, ok)
	require.Equal(t, int64(len(data)), rr.Size)
}
