// Copyright (C) MongoDB, Inc. 2014-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bsonutil

import (
	"testing"

	"github.com/huimingz/mongo-tools/common/json"
	"github.com/huimingz/mongo-tools/common/testtype"
	. "github.com/smartystreets/goconvey/convey"
)

func TestNumberLongValue(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)

	Convey("When converting JSON with NumberLong values", t, func() {

		Convey("works for NumberLong constructor", func() {
			key := "key"
			jsonMap := map[string]interface{}{
				key: json.NumberLong(42),
			}

			err := ConvertLegacyExtJSONDocumentToBSON(jsonMap)
			So(err, ShouldBeNil)
			So(jsonMap[key], ShouldEqual, int64(42))
		})

		Convey(`works for NumberLong document ('{ "$numberLong": "42" }')`, func() {
			key := "key"
			jsonMap := map[string]interface{}{
				key: map[string]interface{}{
					"$numberLong": "42",
				},
			}

			err := ConvertLegacyExtJSONDocumentToBSON(jsonMap)
			So(err, ShouldBeNil)
			So(jsonMap[key], ShouldEqual, int64(42))
		})
	})
}
