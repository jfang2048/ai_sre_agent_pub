package artifacts

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeS3Client struct {
	objects    map[string][]byte
	deleteErrs map[string]error
}

func newFakeS3Client() *fakeS3Client {
	return &fakeS3Client{objects: map[string][]byte{}, deleteErrs: map[string]error{}}
}

func (f *fakeS3Client) Put(_ context.Context, bucket, key string, payload []byte) error {
	f.objects[bucket+"/"+key] = append([]byte(nil), payload...)
	return nil
}

func (f *fakeS3Client) Get(_ context.Context, bucket, key string) ([]byte, error) {
	payload, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, ErrPayloadNotFound
	}
	return append([]byte(nil), payload...), nil
}

func (f *fakeS3Client) Delete(_ context.Context, bucket, key string) error {
	if err, ok := f.deleteErrs[bucket+"/"+key]; ok {
		return err
	}
	if _, ok := f.objects[bucket+"/"+key]; !ok {
		return ErrPayloadNotFound
	}
	delete(f.objects, bucket+"/"+key)
	return nil
}

func TestS3PayloadStorePutGetDelete(t *testing.T) {
	t.Parallel()

	client := newFakeS3Client()
	store, err := newS3PayloadStoreWithClient(Config{
		PayloadS3Bucket: "artifacts",
		PayloadS3Prefix: "agent",
	}, client)
	require.NoError(t, err)

	result, err := store.Write(context.Background(), "messages/run-a/history.json", []byte(`{"run_id":"run-a"}`))
	require.NoError(t, err)
	require.Equal(t, int64(18), result.SizeBytes)
	require.NotEmpty(t, result.Checksum)
	require.Equal(t, "s3://artifacts/agent", store.RootPath())
	require.True(t, store.SharedSurvivable())

	payload, err := store.Read(context.Background(), "messages/run-a/history.json")
	require.NoError(t, err)
	require.JSONEq(t, `{"run_id":"run-a"}`, string(payload))
	require.Contains(t, client.objects, "artifacts/agent/messages/run-a/history.json")

	require.NoError(t, store.Delete(context.Background(), "messages/run-a/history.json"))
	_, err = store.Read(context.Background(), "messages/run-a/history.json")
	require.ErrorIs(t, err, ErrPayloadNotFound)
}
