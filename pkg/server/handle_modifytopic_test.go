package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/haraqa/haraqa/internal/headers"
	"github.com/pkg/errors"
)

func TestServer_HandleModifyTopic(t *testing.T) {
	topic := "modified_topic"
	info := headers.TopicInfo{MinOffset: 123, MaxOffset: 456}
	t.Run("nil body",
		handleModifyTopic(http.StatusBadRequest, headers.ErrInvalidBodyMissing, topic, info, nil, nil))
	t.Run("invalid topic",
		handleModifyTopic(http.StatusBadRequest, headers.ErrInvalidTopic, "", info, bytes.NewBuffer([]byte("{}")), nil))
	t.Run("invalid json",
		handleModifyTopic(http.StatusBadRequest, headers.ErrInvalidBodyJSON, topic, info, bytes.NewBuffer([]byte("hello")), nil))
	t.Run("empty json",
		handleModifyTopic(http.StatusNoContent, nil, topic, info, bytes.NewBuffer([]byte("{}")), nil))
	t.Run("happy path",
		handleModifyTopic(http.StatusOK, nil, topic, info, bytes.NewBuffer([]byte(`{"truncate":123}`)), func(q *MockQueue) {
			q.EXPECT().ModifyTopic(topic, gomock.Any()).Return(&headers.TopicInfo{MinOffset: 123, MaxOffset: 456}, nil).Times(1)
		}))
	t.Run("topic doesn't exist",
		handleModifyTopic(http.StatusPreconditionFailed, headers.ErrTopicDoesNotExist, topic, info, bytes.NewBuffer([]byte(`{"truncate":123}`)), func(q *MockQueue) {
			q.EXPECT().ModifyTopic(topic, gomock.Any()).Return(nil, headers.ErrTopicDoesNotExist).Times(1)
		}))
	errUnknown := errors.New("test modify error")
	t.Run("unknown error",
		handleModifyTopic(http.StatusInternalServerError, errUnknown, topic, info, bytes.NewBuffer([]byte(`{"truncate":123}`)), func(q *MockQueue) {
			q.EXPECT().ModifyTopic(topic, gomock.Any()).Return(nil, errUnknown).Times(1)
		}))
}

func handleModifyTopic(status int, errExpected error, topic string, info headers.TopicInfo, body io.Reader, expect func(q *MockQueue)) func(t *testing.T) {
	return func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		// setup mock queue
		q := NewMockQueue(ctrl)
		q.EXPECT().RootDir().Times(1).Return("")
		q.EXPECT().Close().Return(nil).Times(1)
		if expect != nil {
			expect(q)
		}

		// setup server
		s, err := NewServer(WithQueue(q))
		if err != nil {
			t.Error(err)
			return
		}
		defer s.Close()

		// create request
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodPatch, "/topics/"+topic, body)
		if err != nil {
			t.Error(err)
			return
		}

		// handle
		_, err = getTopic(r)
		if err != nil {
			s.HandleModifyTopic(w, r)
		} else {
			s.ServeHTTP(w, r)
		}

		// check result
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != status {
			t.Error(resp.Status)
		}
		err = headers.ReadErrors(resp.Header)
		if err != errExpected && err.Error() != errExpected.Error() {
			t.Error(err)
		}
		if err != nil || status == http.StatusNoContent {
			return
		}

		// check body
		var v headers.TopicInfo
		err = json.NewDecoder(resp.Body).Decode(&v)
		if err != nil {
			t.Error(err)
			return
		}
		if !reflect.DeepEqual(v, info) {
			t.Error(v, info)
		}
	}
}
