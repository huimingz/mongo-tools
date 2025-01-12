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
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestUndefinedValue(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)

	Convey("When converting JSON with undefined values", t, func() {

		Convey("works for undefined literal", func() {
			key := "key"
			jsonMap := map[string]interface{}{
				key: json.Undefined{},
			}

			err := ConvertLegacyExtJSONDocumentToBSON(jsonMap)
			So(err, ShouldBeNil)
			So(jsonMap[key], ShouldResemble, primitive.Undefined{})
		})

		Convey(`works for undefined document ('{ "$undefined": true }')`, func() {
			key := "key"
			jsonMap := map[string]interface{}{
				key: map[string]interface{}{
					"$undefined": true,
				},
			}

			err := ConvertLegacyExtJSONDocumentToBSON(jsonMap)
			So(err, ShouldBeNil)
			So(jsonMap[key], ShouldResemble, primitive.Undefined{})
		})
	})
}
