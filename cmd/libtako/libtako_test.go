package main

import (
	"encoding/json"
	"testing"
)

type testLink struct {
	Code    string `json:"code"`
	URL     string `json:"url"`
	Clicks  int64  `json:"clicks"`
	OwnerID string `json:"owner_id"`
}

func TestLibtakoGoFunctions(t *testing.T) {
	appID := tako_c_testapp_new()
	if appID == 0 {
		t.Fatalf("failed to create test app via tako_c_testapp_new")
	}
	defer tako_c_testapp_close(appID)

	link := testLink{
		Code:    "test1",
		URL:     "https://example.com/test1",
		Clicks:  42,
		OwnerID: "alice",
	}
	data, err := json.Marshal(link)
	if err != nil {
		t.Fatal(err)
	}

	cKind := makeCString("links")
	defer freeCString(cKind)
	cKey := makeCString("test1")
	defer freeCString(cKey)
	cID := makeCString("code")
	defer freeCString(cID)
	cIdx := makeCString("clicks,owner_id")
	defer freeCString(cIdx)

	tako_c_entity_register(cKind, cID, cIdx, nil)

	cData := makeCBytes(data)
	defer freeCString(cData)

	putRes := tako_c_entity_put(0, cKind, cKey, cData, makeCInt32(len(data)))
	defer tako_c_free(putRes)
	if putRes.status != 0 {
		t.Fatalf("tako_c_entity_put failed: %s", goString(putRes.err))
	}

	getRes := tako_c_entity_get(0, cKind, cKey)
	defer tako_c_free(getRes)
	if getRes.status != 0 {
		t.Fatalf("tako_c_entity_get failed: %s", goString(getRes.err))
	}

	gotBytes := goBytes(getRes.data, getRes.len)
	var gotLink testLink
	if err := json.Unmarshal(gotBytes, &gotLink); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if gotLink.URL != link.URL || gotLink.Clicks != 42 {
		t.Fatalf("unexpected link data: %+v", gotLink)
	}

	// Cache test
	cCache := makeCString("test_cache")
	defer freeCString(cCache)
	cCacheKey := makeCString("k1")
	defer freeCString(cCacheKey)
	cCacheVal := makeCString("v1")
	defer freeCString(cCacheVal)

	cSetRes := tako_c_cache_set(0, cCache, cCacheKey, cCacheVal, makeCInt32(2), makeCInt32(60))
	defer tako_c_free(cSetRes)
	if cSetRes.status != 0 {
		t.Fatalf("cache set failed: %s", goString(cSetRes.err))
	}

	cGetRes := tako_c_cache_get(0, cCache, cCacheKey)
	defer tako_c_free(cGetRes)
	if cGetRes.status != 0 {
		t.Fatalf("cache get failed: %s", goString(cGetRes.err))
	}
	if string(goBytes(cGetRes.data, cGetRes.len)) != "v1" {
		t.Fatalf("expected v1, got %s", string(goBytes(cGetRes.data, cGetRes.len)))
	}
}
