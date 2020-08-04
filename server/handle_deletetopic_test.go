package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/gorilla/mux"
	"github.com/haraqa/haraqa/protocol"
	"github.com/haraqa/haraqa/server/queue"
	"github.com/pkg/errors"
)

func TestServer_HandleDeleteTopic(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	topic := "deleted_topic"
	q := queue.NewMockQueue(ctrl)
	gomock.InOrder(
		q.EXPECT().DeleteTopic(topic).Return(nil).Times(1),
		q.EXPECT().DeleteTopic(topic).Return(protocol.ErrTopicDoesNotExist).Times(1),
		q.EXPECT().DeleteTopic(topic).Return(errors.New("test delete error")).Times(1),
	)
	s := Server{q: q}

	// invalid topic
	{
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodPut, "/topics/", bytes.NewBuffer([]byte("test body")))
		if err != nil {
			t.Fatal(err)
		}

		s.HandleDeleteTopic()(w, r)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatal(resp.Status)
		}
		err = protocol.ReadErrors(resp.Header)
		if err != protocol.ErrInvalidTopic {
			t.Fatal(err)
		}
	}

	// valid topic, happy path
	{
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodDelete, "/topics/"+topic, bytes.NewBuffer([]byte("test body")))
		if err != nil {
			t.Fatal(err)
		}
		r = mux.SetURLVars(r, map[string]string{"topic": topic})
		s.HandleDeleteTopic()(w, r)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatal(resp.Status)
		}
		err = protocol.ReadErrors(resp.Header)
		if err != nil {
			t.Fatal(err)
		}
	}

	// valid topic, queue error: topic exists
	{
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodDelete, "/topics/"+topic, bytes.NewBuffer([]byte("test body")))
		if err != nil {
			t.Fatal(err)
		}
		r = mux.SetURLVars(r, map[string]string{"topic": topic})
		s.HandleDeleteTopic()(w, r)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusPreconditionFailed {
			t.Fatal(resp.Status)
		}
		err = protocol.ReadErrors(resp.Header)
		if err != protocol.ErrTopicDoesNotExist {
			t.Fatal(err)
		}
	}

	// valid topic, queue error: unknown error
	{
		w := httptest.NewRecorder()
		r, err := http.NewRequest(http.MethodDelete, "/topics/"+topic, bytes.NewBuffer([]byte("test body")))
		if err != nil {
			t.Fatal(err)
		}
		r = mux.SetURLVars(r, map[string]string{"topic": topic})
		s.HandleDeleteTopic()(w, r)
		resp := w.Result()
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatal(resp.Status)
		}
		err = protocol.ReadErrors(resp.Header)
		if err.Error() != "test delete error" {
			t.Fatal(err)
		}
	}
}
