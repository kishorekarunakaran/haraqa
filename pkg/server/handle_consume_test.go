package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/haraqa/haraqa/internal/headers"
	"github.com/pkg/errors"
)

func TestServer_HandleConsume(t *testing.T) {
	topic := "consumer_topic"
	group := "group"
	t.Run("invalid topic",
		handleConsume(group, http.StatusBadRequest, headers.ErrInvalidTopic, "/topics/", nil))
	t.Run("missing id",
		handleConsume(group, http.StatusBadRequest, headers.ErrInvalidMessageID, "/topics/"+topic, nil))
	t.Run("invalid id",
		handleConsume(group, http.StatusBadRequest, headers.ErrInvalidMessageID, "/topics/"+topic+"?id=invalid", nil))
	t.Run("invalid limit",
		handleConsume(group, http.StatusBadRequest, headers.ErrInvalidMessageLimit, "/topics/"+topic+"?id=123&limit=invalid", nil))
	t.Run("topic doesn't exist",
		handleConsume(group, http.StatusPreconditionFailed, headers.ErrTopicDoesNotExist, "/topics/"+topic+"?id=123", func(q *MockQueue) {
			q.EXPECT().Consume(group, topic, int64(123), int64(-1), gomock.Any()).Return(0, headers.ErrTopicDoesNotExist).Times(1)
		}))
	t.Run("happy path: limit == -1",
		handleConsume(group, http.StatusPartialContent, nil, "/topics/"+topic+"?id=123", func(q *MockQueue) {
			q.EXPECT().Consume(group, topic, int64(123), int64(-1), gomock.Any()).
				DoAndReturn(func(group, topic string, offset, limit int64, w http.ResponseWriter) (int, error) {
					w.WriteHeader(http.StatusPartialContent)
					return 10, nil
				}).Times(1)
		}))
	t.Run("happy path: limit == 0",
		handleConsume(group, http.StatusOK, nil, "/topics/"+topic+"?id=123&limit=0", func(q *MockQueue) {
			q.EXPECT().Consume(group, topic, int64(123), int64(-1), gomock.Any()).Return(10, nil).Times(1)
		}))
	t.Run("no content",
		handleConsume(group, http.StatusNoContent, headers.ErrNoContent, "/topics/"+topic+"?id=123", func(q *MockQueue) {
			q.EXPECT().Consume(group, topic, int64(123), int64(-1), gomock.Any()).Return(0, nil).Times(1)
		}))
	errUnknown := errors.New("some unexpected error")
	t.Run("unknown error",
		handleConsume(group, http.StatusInternalServerError, errUnknown, "/topics/"+topic+"?id=123", func(q *MockQueue) {
			q.EXPECT().Consume(group, topic, int64(123), int64(-1), gomock.Any()).Return(0, errUnknown).Times(1)
		}))
}

func handleConsume(group string, status int, errExpected error, url string, expect func(q *MockQueue)) func(*testing.T) {
	return func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// setup queue
		q := NewMockQueue(ctrl)
		q.EXPECT().RootDir().Times(1).Return("")
		q.EXPECT().Close().Times(1).Return(nil)
		if expect != nil {
			expect(q)
		}

		// setup server
		s, err := NewServer(WithQueue(q))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		// make request/response
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodGet, url, bytes.NewBuffer([]byte("test body")))
		if err != nil {
			t.Error(err)
			return
		}
		r.Header.Set(headers.HeaderConsumerGroup, group)

		// if no topic, handle directly
		_, err = getTopic(r)
		if err != nil {
			s.HandleConsume(w, r)
		} else {
			s.ServeHTTP(w, r)
		}

		// check results
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Error(resp.Status, status)
		}
		err = headers.ReadErrors(resp.Header)
		if err != errExpected && err.Error() != errExpected.Error() {
			t.Error(err)
		}
	}
}
