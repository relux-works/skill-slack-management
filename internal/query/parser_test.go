package query

import "testing"

func TestParseBatchSupportsBatchingAndQuotedParams(t *testing.T) {
	requests, err := ParseBatch(`schema(); attachment("att-1") { full }`)
	if err != nil {
		t.Fatalf("ParseBatch() error = %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if requests[1].Operation != "attachment" || requests[1].Positional != "att-1" {
		t.Fatalf("attachment request = %#v", requests[1])
	}
	if len(requests[1].Fields) != 1 || requests[1].Fields[0] != "full" {
		t.Fatalf("attachment fields = %#v", requests[1].Fields)
	}
}

func TestParseBatchRejectsDuplicateParameters(t *testing.T) {
	if _, err := ParseBatch(`post_message(C123, text="first", text="second")`); err == nil {
		t.Fatal("ParseBatch() accepted a duplicate parameter")
	}
}
