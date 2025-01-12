// Copyright (C) MongoDB, Inc. 2014-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongoimport

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/huimingz/mongo-tools/common/db"
	"github.com/huimingz/mongo-tools/common/options"
	"github.com/huimingz/mongo-tools/common/testtype"
	"github.com/huimingz/mongo-tools/common/testutil"
	"github.com/huimingz/mongo-tools/common/util"
	. "github.com/smartystreets/goconvey/convey"
	"go.mongodb.org/mongo-driver/bson"
	mopt "go.mongodb.org/mongo-driver/mongo/options"
)

const (
	testDb         = "db"
	testCollection = "c"
	mioSoeFile     = "testdata/10k1dup10k.json"
)

// checkOnlyHasDocuments returns an error if the documents in the test
// collection don't exactly match those that are passed in
func checkOnlyHasDocuments(sessionProvider *db.SessionProvider, expectedDocuments []bson.M) error {
	session, err := sessionProvider.GetSession()
	if err != nil {
		return err
	}

	collection := session.Database(testDb).Collection(testCollection)
	cursor, err := collection.Find(nil, bson.D{}, mopt.Find().SetSort(bson.D{{"_id", 1}}))
	if err != nil {
		return err
	}

	var docs []bson.M
	for cursor.Next(nil) {
		var doc bson.M
		if err = cursor.Decode(&doc); err != nil {
			return err
		}

		docs = append(docs, doc)
	}
	if len(docs) != len(expectedDocuments) {
		return fmt.Errorf("document count mismatch: expected %#v, got %#v",
			len(expectedDocuments), len(docs))
	}

	for index := range docs {
		if !reflect.DeepEqual(docs[index], expectedDocuments[index]) {
			return fmt.Errorf("document mismatch: expected %#v, got %#v",
				expectedDocuments[index], docs[index])
		}
	}

	return nil
}

func countDocuments(sessionProvider *db.SessionProvider) (int, error) {
	session, err := (*sessionProvider).GetSession()
	if err != nil {
		return 0, err
	}

	collection := session.Database(testDb).Collection(testCollection)
	n, err := collection.CountDocuments(nil, bson.D{})
	if err != nil {
		return 0, err
	}

	return int(n), nil
}

// getBasicToolOptions returns a test helper to instantiate the session provider
// for calls to StreamDocument
func getBasicToolOptions() *options.ToolOptions {
	general := &options.General{}
	ssl := testutil.GetSSLOptions()
	auth := testutil.GetAuthOptions()
	namespace := &options.Namespace{
		DB:         testDb,
		Collection: testCollection,
	}
	connection := &options.Connection{
		Host: "localhost",
		Port: db.DefaultTestPort,
	}

	return &options.ToolOptions{
		General:    general,
		SSL:        &ssl,
		Namespace:  namespace,
		Connection: connection,
		Auth:       &auth,
		URI:        &options.URI{},
	}
}

func NewMongoImport() (*MongoImport, error) {
	toolOptions := getBasicToolOptions()
	inputOptions := &InputOptions{
		ParseGrace: "stop",
	}
	ingestOptions := &IngestOptions{}
	provider, err := db.NewSessionProvider(*toolOptions)
	if err != nil {
		return nil, err
	}
	return &MongoImport{
		ToolOptions:     toolOptions,
		InputOptions:    inputOptions,
		IngestOptions:   ingestOptions,
		SessionProvider: provider,
	}, nil
}

// NewMockMongoImport gets an instance of MongoImport with no underlying SessionProvider.
// Use this for tests that don't communicate with the server (e.g. options parsing tests)
func NewMockMongoImport() *MongoImport {
	toolOptions := getBasicToolOptions()
	inputOptions := &InputOptions{
		ParseGrace: "stop",
	}
	ingestOptions := &IngestOptions{}

	return &MongoImport{
		ToolOptions:     toolOptions,
		InputOptions:    inputOptions,
		IngestOptions:   ingestOptions,
		SessionProvider: nil,
	}
}

func getImportWithArgs(additionalArgs ...string) (*MongoImport, error) {
	opts, err := ParseOptions(append(testutil.GetBareArgs(), additionalArgs...), "", "")
	if err != nil {
		return nil, fmt.Errorf("error parsing args: %v", err)
	}

	imp, err := New(opts)
	if err != nil {
		return nil, fmt.Errorf("error making new instance of mongorestore: %v", err)
	}

	return imp, nil
}

func TestSplitInlineHeader(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)
	Convey("handle normal, untyped headers", t, func() {
		fields := []string{"foo.bar", "baz", "boo"}
		header := strings.Join(fields, ",")
		Convey("with '"+header+"'", func() {
			So(splitInlineHeader(header), ShouldResemble, fields)
		})
	})
	Convey("handle typed headers", t, func() {
		fields := []string{"foo.bar.string()", "baz.date(January 2 2006)", "boo.binary(hex)"}
		header := strings.Join(fields, ",")
		Convey("with '"+header+"'", func() {
			So(splitInlineHeader(header), ShouldResemble, fields)
		})
	})
	Convey("handle typed headers that include commas", t, func() {
		fields := []string{"foo.bar.date(,,,,)", "baz.date(January 2, 2006)", "boo.binary(hex)"}
		header := strings.Join(fields, ",")
		Convey("with '"+header+"'", func() {
			So(splitInlineHeader(header), ShouldResemble, fields)
		})
	})
}

func TestMongoImportValidateSettings(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)

	Convey("Given a mongoimport instance for validation, ", t, func() {
		Convey("an error should be thrown if no collection is given", func() {
			imp := NewMockMongoImport()
			imp.ToolOptions.Namespace.DB = ""
			imp.ToolOptions.Namespace.Collection = ""
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if an invalid type is given", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = "invalid"
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if neither --headerline is supplied "+
			"nor --fields/--fieldFile", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("no error should be thrown if --headerline is not supplied "+
			"but --fields is supplied", func() {
			imp := NewMockMongoImport()
			fields := "a,b,c"
			imp.InputOptions.Fields = &fields
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("no error should be thrown if no input type is supplied", func() {
			imp := NewMockMongoImport()
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("no error should be thrown if there's just one positional argument", func() {
			imp := NewMockMongoImport()
			So(imp.validateSettings([]string{"a"}), ShouldBeNil)
		})

		Convey("no error should be thrown if --file is used with one positional argument", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.File = "abc"
			So(imp.validateSettings([]string{"a"}), ShouldBeNil)
		})

		Convey("no error should be thrown if there's more than one positional argument", func() {
			imp := NewMockMongoImport()
			So(imp.validateSettings([]string{"a", "b"}), ShouldBeNil)
		})

		Convey("an error should be thrown if --headerline is used with JSON input", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if --fields is used with JSON input", func() {
			imp := NewMockMongoImport()
			fields := ""
			imp.InputOptions.Fields = &fields
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
			fields = "a,b,c"
			imp.InputOptions.Fields = &fields
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if --fieldFile is used with JSON input", func() {
			imp := NewMockMongoImport()
			fieldFile := ""
			imp.InputOptions.FieldFile = &fieldFile
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
			fieldFile = "test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if --ignoreBlanks is used with JSON input", func() {
			imp := NewMockMongoImport()
			imp.IngestOptions.IgnoreBlanks = true
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("no error should be thrown if --headerline is not supplied "+
			"but --fieldFile is supplied", func() {
			imp := NewMockMongoImport()
			fieldFile := "test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("an error should be thrown if --mode is incorrect", func() {
			imp := NewMockMongoImport()
			imp.IngestOptions.Mode = "wrong"
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("an error should be thrown if a field in the --upsertFields "+
			"argument starts with a dollar sign", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.Mode = modeUpsert
			imp.IngestOptions.UpsertFields = "a,$b,c"
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
			imp.IngestOptions.UpsertFields = "a,.b,c"
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("no error should be thrown if --upsertFields is supplied without "+
			"--mode=xxx", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.UpsertFields = "a,b,c"
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.IngestOptions.Mode, ShouldEqual, modeUpsert)
		})

		Convey("an error should be thrown if --upsertFields is used with "+
			"--mode=insert", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.Mode = modeInsert
			imp.IngestOptions.UpsertFields = "a"
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("if --mode=upsert is used without --upsertFields, _id should be set as "+
			"the upsert field", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.Mode = modeUpsert
			imp.IngestOptions.UpsertFields = ""
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.upsertFields, ShouldResemble, []string{"_id"})
		})

		Convey("if --mode=delete is used without --upsertFields, _id should be set as "+
			"the upsert field", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.Mode = modeDelete
			imp.IngestOptions.UpsertFields = ""
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.upsertFields, ShouldResemble, []string{"_id"})
		})

		Convey("no error should be thrown if all fields in the --upsertFields "+
			"argument are valid", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.IngestOptions.Mode = modeUpsert
			imp.IngestOptions.UpsertFields = "a,b,c"
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("no error should be thrown if --fields is supplied with CSV import", func() {
			imp := NewMockMongoImport()
			fields := "a,b,c"
			imp.InputOptions.Fields = &fields
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("an error should be thrown if an empty --fields is supplied with CSV import", func() {
			imp := NewMockMongoImport()
			fields := ""
			imp.InputOptions.Fields = &fields
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("no error should be thrown if --fieldFile is supplied with CSV import", func() {
			imp := NewMockMongoImport()
			fieldFile := "test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			imp.InputOptions.Type = CSV
			So(imp.validateSettings([]string{}), ShouldBeNil)
		})

		Convey("an error should be thrown if no collection and no file is supplied", func() {
			imp := NewMockMongoImport()
			fieldFile := "test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			imp.InputOptions.Type = CSV
			imp.ToolOptions.Namespace.Collection = ""
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})

		Convey("no error should be thrown if --file is used (without -c) supplied "+
			"- the file name should be used as the collection name", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.File = "input"
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.ToolOptions.Namespace.Collection = ""
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.ToolOptions.Namespace.Collection, ShouldEqual,
				imp.InputOptions.File)
		})

		Convey("with no collection name and a file name the base name of the "+
			"file (without the extension) should be used as the collection name", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
			imp.InputOptions.HeaderLine = true
			imp.InputOptions.Type = CSV
			imp.ToolOptions.Namespace.Collection = ""
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.ToolOptions.Namespace.Collection, ShouldEqual, "input")
		})

		Convey("error should be thrown if --legacy is specified and input type is not JSON", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = CSV
			fieldFile := "test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			imp.InputOptions.Legacy = true
			So(imp.validateSettings([]string{}), ShouldNotBeNil)
		})
	})
}

func TestGetSourceReader(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)
	Convey("Given a mongoimport instance, on calling getSourceReader", t,
		func() {
			Convey("an error should be thrown if the given file referenced by "+
				"the reader does not exist", func() {
				imp := NewMockMongoImport()
				imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
				imp.InputOptions.Type = CSV
				imp.ToolOptions.Namespace.Collection = ""
				_, _, err := imp.getSourceReader()
				So(err, ShouldNotBeNil)
			})

			Convey("no error should be thrown if the file exists", func() {
				imp := NewMockMongoImport()
				imp.InputOptions.File = "testdata/test_array.json"
				imp.InputOptions.Type = JSON
				_, _, err := imp.getSourceReader()
				So(err, ShouldBeNil)
			})

			Convey("no error should be thrown if stdin is used", func() {
				imp := NewMockMongoImport()
				imp.InputOptions.File = ""
				_, _, err := imp.getSourceReader()
				So(err, ShouldBeNil)
			})
		})
}

func TestGetInputReader(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)
	Convey("Given a io.Reader on calling getInputReader", t, func() {
		Convey("should parse --fields using valid csv escaping", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Fields = new(string)
			*imp.InputOptions.Fields = "foo.auto(),bar.date(January 2, 2006)"
			imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
			imp.InputOptions.ColumnsHaveTypes = true
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("should complain about non-escaped new lines in --fields", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Fields = new(string)
			*imp.InputOptions.Fields = "foo.auto(),\nblah.binary(hex),bar.date(January 2, 2006)"
			imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
			imp.InputOptions.ColumnsHaveTypes = true
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("no error should be thrown if neither --fields nor --fieldFile "+
			"is used", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("no error should be thrown if --fields is used", func() {
			imp := NewMockMongoImport()
			fields := "a,b,c"
			imp.InputOptions.Fields = &fields
			imp.InputOptions.File = "/path/to/input/file/dot/input.txt"
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("no error should be thrown if --fieldFile is used and it "+
			"references a valid file", func() {
			imp := NewMockMongoImport()
			fieldFile := "testdata/test.csv"
			imp.InputOptions.FieldFile = &fieldFile
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("an error should be thrown if --fieldFile is used and it "+
			"references an invalid file", func() {
			imp := NewMockMongoImport()
			fieldFile := "/path/to/input/file/dot/input.txt"
			imp.InputOptions.FieldFile = &fieldFile
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldNotBeNil)
		})
		Convey("no error should be thrown for CSV import inputs", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = CSV
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("no error should be thrown for TSV import inputs", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = TSV
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("no error should be thrown for JSON import inputs", func() {
			imp := NewMockMongoImport()
			imp.InputOptions.Type = JSON
			_, err := imp.getInputReader(&os.File{})
			So(err, ShouldBeNil)
		})
		Convey("an error should be thrown if --fieldFile fields are invalid", func() {
			imp := NewMockMongoImport()
			fieldFile := "testdata/test_fields_invalid.txt"
			imp.InputOptions.FieldFile = &fieldFile
			file, err := os.Open(fieldFile)
			So(err, ShouldBeNil)
			_, err = imp.getInputReader(file)
			So(err, ShouldNotBeNil)
		})
		Convey("no error should be thrown if --fieldFile fields are valid", func() {
			imp := NewMockMongoImport()
			fieldFile := "testdata/test_fields_valid.txt"
			imp.InputOptions.FieldFile = &fieldFile
			file, err := os.Open(fieldFile)
			So(err, ShouldBeNil)
			_, err = imp.getInputReader(file)
			So(err, ShouldBeNil)
		})
	})
}

func TestImportDocuments(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.IntegrationTestType)
	Convey("With a mongoimport instance", t, func() {
		Reset(func() {
			sessionProvider, err := db.NewSessionProvider(*getBasicToolOptions())
			if err != nil {
				t.Fatalf("error getting session provider session: %v", err)
			}
			session, err := sessionProvider.GetSession()
			if err != nil {
				t.Fatalf("error getting session: %v", err)
			}
			_, err = session.Database(testDb).Collection(testCollection).DeleteMany(nil, bson.D{})
			if err != nil {
				t.Fatalf("error dropping collection: %v", err)
			}
		})
		Convey("no error should be thrown for CSV import on test data and all "+
			"CSV data lines should be imported correctly", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "a,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.WriteConcern = "majority"
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)
		})
		Convey("an error should be thrown for JSON import on test data that is "+
			"JSON array", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.File = "testdata/test_array.json"
			imp.IngestOptions.WriteConcern = "majority"
			numProcessed, _, err := imp.ImportDocuments()
			So(err, ShouldNotBeNil)
			So(numProcessed, ShouldEqual, 0)
		})
		Convey("TOOLS-247: no error should be thrown for JSON import on test "+
			"data and all documents should be imported correctly", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.File = "testdata/test_plain2.json"
			imp.IngestOptions.WriteConcern = "majority"
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 10)
			So(numFailed, ShouldEqual, 0)
		})
		Convey("CSV import with --ignoreBlanks should import only non-blank fields", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_blanks.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.IgnoreBlanks = true

			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2)},
				{"_id": int32(5), "c": "6e"},
				{"_id": int32(7), "b": int32(8), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import without --ignoreBlanks should include blanks", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_blanks.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(numFailed, ShouldEqual, 0)
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": ""},
				{"_id": int32(5), "b": "", "c": "6e"},
				{"_id": int32(7), "b": int32(8), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("no error should be thrown for CSV import on test data with --upsertFields", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.UpsertFields = "b,c"
			imp.IngestOptions.MaintainInsertionOrder = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(numFailed, ShouldEqual, 0)
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("no error should be thrown for CSV import on test data with "+
			"--stopOnError. Only documents before error should be imported", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.StopOnError = true
			imp.IngestOptions.MaintainInsertionOrder = true
			imp.IngestOptions.WriteConcern = "majority"
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import with duplicate _id's should not error if --stopOnError is not set", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_duplicate.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.StopOnError = false
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 4)
			So(numFailed, ShouldEqual, 1)

			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(6)},
				{"_id": int32(8), "b": int32(6), "c": int32(6)},
			}
			// all docs except the one with duplicate _id - should be imported
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("no error should be thrown for CSV import on test data with --drop", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.Drop = true
			imp.IngestOptions.MaintainInsertionOrder = true
			imp.IngestOptions.WriteConcern = "majority"
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(numFailed, ShouldEqual, 0)
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import on test data with --headerLine should succeed", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.InputOptions.HeaderLine = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 2)
			So(numFailed, ShouldEqual, 0)
		})
		Convey("EOF should be thrown for CSV import with --headerLine if file is empty", func() {
			csvFile, err := ioutil.TempFile("", "mongoimport_")
			So(err, ShouldBeNil)
			csvFile.Close()

			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = csvFile.Name()
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.InputOptions.HeaderLine = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldEqual, io.EOF)
			So(numProcessed, ShouldEqual, 0)
			So(numFailed, ShouldEqual, 0)
		})
		Convey("CSV import with --mode=upsert and --upsertFields should succeed", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.UpsertFields = "_id"
			imp.IngestOptions.MaintainInsertionOrder = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "c": int32(2), "b": int32(3)},
				{"_id": int32(3), "c": 5.4, "b": "string"},
				{"_id": int32(5), "c": int32(6), "b": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import with --mode=delete should succeed", func() {
			// First import 3 documents
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.MaintainInsertionOrder = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)

			// Then delete two documents
			imp, err = NewMongoImport()
			So(err, ShouldBeNil)

			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_delete.csv"
			fields = "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.Mode = modeDelete
			imp.IngestOptions.StopOnError = true
			// Must specify upsert fields since option parsing is skipped in tests
			imp.upsertFields = []string{"_id"}
			numProcessed, numFailed, err = imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 2)
			So(numFailed, ShouldEqual, 0)

			expectedDocuments := []bson.M{
				{"_id": int32(3), "c": 5.4, "b": "string"},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import with --mode=delete and --upsertFields should succeed", func() {
			// First import 3 documents
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.MaintainInsertionOrder = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)

			// Then delete two documents
			imp, err = NewMongoImport()
			So(err, ShouldBeNil)

			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_delete.csv"
			fields = "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.Mode = modeDelete
			imp.IngestOptions.StopOnError = true
			// Must specify upsert fields since option parsing is skipped in tests
			imp.upsertFields = []string{"b", "c"}
			numProcessed, numFailed, err = imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 1)
			So(numFailed, ShouldEqual, 0)

			expectedDocuments := []bson.M{
				{"_id": int32(3), "c": 5.4, "b": "string"},
				{"_id": int32(5), "c": int32(6), "b": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import with --mode=delete and --ignoreBlanks should not take any action for "+
			"documents that have blank values for upsert fields", func() {
			// First import 3 documents
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test.csv"
			fields := "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.MaintainInsertionOrder = true
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 0)

			// Then delete two documents
			imp, err = NewMongoImport()
			So(err, ShouldBeNil)

			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_delete_with_blanks.csv"
			fields = "_id,c,b"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.Mode = modeDelete
			imp.IngestOptions.IgnoreBlanks = true
			imp.IngestOptions.StopOnError = true
			// Must specify upsert fields since option parsing is skipped in tests
			imp.upsertFields = []string{"c"}
			numProcessed, numFailed, err = imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 1)
			So(numFailed, ShouldEqual, 0)

			expectedDocuments := []bson.M{
				{"_id": int32(3), "c": 5.4, "b": "string"},
				{"_id": int32(5), "c": int32(6), "b": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("CSV import with --mode=upsert/--upsertFields with duplicate id should succeed "+
			"even if stopOnError is set", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_duplicate.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.Mode = modeUpsert
			imp.IngestOptions.StopOnError = true
			imp.upsertFields = []string{"_id"}
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 5)
			So(numFailed, ShouldEqual, 0)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(9)},
				{"_id": int32(8), "b": int32(6), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("an error should be thrown for CSV import on test data with "+
			"duplicate _id if --stopOnError is set", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_duplicate.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.StopOnError = true
			imp.IngestOptions.WriteConcern = "1"
			imp.IngestOptions.MaintainInsertionOrder = true
			numInserted, numFailed, err := imp.ImportDocuments()
			So(err, ShouldNotBeNil)
			So(numInserted, ShouldEqual, 3)
			So(numFailed, ShouldEqual, 1)
			expectedDocuments := []bson.M{
				{"_id": int32(1), "b": int32(2), "c": int32(3)},
				{"_id": int32(3), "b": 5.4, "c": "string"},
				{"_id": int32(5), "b": int32(6), "c": int32(6)},
			}
			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		})
		Convey("an error should be thrown for JSON import on test data that "+
			"is a JSON array without passing --jsonArray", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.File = "testdata/test_array.json"
			imp.IngestOptions.WriteConcern = "1"
			numInserted, _, err := imp.ImportDocuments()
			So(err, ShouldNotBeNil)
			So(numInserted, ShouldEqual, 0)
		})
		Convey("an error should be thrown if a plain JSON file is supplied", func() {
			fileHandle, err := os.Open("testdata/test_plain.json")
			So(err, ShouldBeNil)
			jsonInputReader := NewJSONInputReader(true, true, fileHandle, 1)
			docChan := make(chan bson.D, 1)
			So(jsonInputReader.StreamDocument(true, docChan), ShouldNotBeNil)
		})
		Convey("an error should be thrown for invalid CSV import on test data", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.IngestOptions.Mode = modeInsert
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_bad.csv"
			fields := "_id,b,c"
			imp.InputOptions.Fields = &fields
			imp.IngestOptions.StopOnError = true
			imp.IngestOptions.WriteConcern = "1"
			imp.IngestOptions.MaintainInsertionOrder = true
			imp.IngestOptions.BulkBufferSize = 1
			_, _, err = imp.ImportDocuments()
			So(err, ShouldNotBeNil)
		})
		Convey("CSV import with --mode=upsert/--upsertFields with a nested upsert field should succeed when repeated", func() {
			imp, err := NewMongoImport()
			So(err, ShouldBeNil)
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_nested_upsert.csv"
			imp.InputOptions.HeaderLine = true
			imp.IngestOptions.Mode = modeUpsert
			imp.upsertFields = []string{"level1.level2.key1"}
			numProcessed, numFailed, err := imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 1)
			So(numFailed, ShouldEqual, 0)
			n, err := countDocuments(imp.SessionProvider)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 1)

			// Repeat must succeed
			imp, err = NewMongoImport()
			So(err, ShouldBeNil)
			imp.InputOptions.Type = CSV
			imp.InputOptions.File = "testdata/test_nested_upsert.csv"
			imp.InputOptions.HeaderLine = true
			imp.IngestOptions.Mode = modeUpsert
			imp.upsertFields = []string{"level1.level2.key1"}
			numProcessed, numFailed, err = imp.ImportDocuments()
			So(err, ShouldBeNil)
			So(numProcessed, ShouldEqual, 1)
			So(numFailed, ShouldEqual, 0)
			n, err = countDocuments(imp.SessionProvider)
			So(err, ShouldBeNil)
			So(n, ShouldEqual, 1)
		})
		Convey("With --useArrayIndexFields: Top-level numerical fields should be document keys",
			nestedFieldsTestHelper(
				"_id,0,1\n1,2,3",
				[]bson.M{
					{"_id": int32(1), "0": int32(2), "1": int32(3)},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert nested document",
			nestedFieldsTestHelper(
				"_id,a.a,a.b\n1,2,3",
				[]bson.M{
					{"_id": int32(1), "a": bson.M{"a": int32(2), "b": int32(3)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert an array",
			nestedFieldsTestHelper(
				"_id,a.0,a.1\n1,2,3",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{int32(2), int32(3)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert an array of documents",
			nestedFieldsTestHelper(
				"_id,a.0.a,a.0.b,a.1.a\n1,2,3,4",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{bson.M{"a": int32(2), "b": int32(3)}, bson.M{"a": int32(4)}}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert an array of arrays",
			nestedFieldsTestHelper(
				"_id,a.0.0,a.0.1,a.1.0\n1,2,3,4",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{bson.A{int32(2), int32(3)}, bson.A{int32(4)}}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert an array when top-level key is \"0\"",
			nestedFieldsTestHelper(
				"_id,0.0,0.1\n1,2,3",
				[]bson.M{
					{"_id": int32(1), "0": bson.A{int32(2), int32(3)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should insert an array in a document in an array",
			nestedFieldsTestHelper(
				"_id,a.0.a.0,a.0.a.1\n1,2,3",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{bson.M{"a": bson.A{int32(2), int32(3)}}}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: If an array element is blank in the csv file, an empty string should be inserted",
			nestedFieldsTestHelper(
				"_id,a.0,a.1,a.2\n1,2,,4",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{int32(2), "", int32(4)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: If an array with more than 10 fields should be inserted",
			nestedFieldsTestHelper(
				"_id,a.0,a.1,a.2,a.3,a.4,a.5,a.6,a.7,a.8,a.9,a.10\n0,1,2,3,4,5,6,7,8,9,10",
				[]bson.M{
					{"_id": int32(0), "a": bson.A{int32(1), int32(2), int32(3), int32(4), int32(5), int32(6), int32(7), int32(8), int32(9), int32(10)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: An number with leading zeros should be interpreted as a document key, not an index",
			nestedFieldsTestHelper(
				"_id,a.0001\n1,2",
				[]bson.M{
					{"_id": int32(1), "a": bson.M{"0001": int32(2)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: An number with leading plus should be interpreted as a document key, not an index",
			nestedFieldsTestHelper(
				"_id,a.+15558675309\n1,2",
				[]bson.M{
					{"_id": int32(1), "a": bson.M{"+15558675309": int32(2)}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Should be able to make changes to document in an array once document has been created",
			nestedFieldsTestHelper(
				"_id,a.0.a,a.1.a,a.0.b\n1,2,3,4",
				[]bson.M{
					{"_id": int32(1), "a": bson.A{bson.M{"a": int32(2), "b": int32(4)}, bson.M{"a": int32(3)}}},
				},
				nil,
			),
		)
		Convey("With --useArrayIndexFields: Duplicate fields should throw an error",
			nestedFieldsTestHelper(
				"_id,a.0,a.0\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'a.0': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Array fields not starting at 0 should throw an error",
			nestedFieldsTestHelper(
				"_id,a.1,a.0\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'a.1': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Array fields skipping an index should throw an error",
			nestedFieldsTestHelper(
				"_id,a.0,a.2\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'a.2': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Array fields with sub documents skipping an index should throw an error",
			nestedFieldsTestHelper(
				"_id,a.0.a,a.2.a\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'a.2.a': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Array field should throw an error if value has already been set as document",
			nestedFieldsTestHelper(
				"_id,a.a,a.0\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a' and 'a.0' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Array field should throw an error if value has already been set as document (deep object)",
			nestedFieldsTestHelper(
				"_id,a.a.a.a,a.a.0.a\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a.a.a' and 'a.a.0.a' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Document field should throw an error if value has already been set as array",
			nestedFieldsTestHelper(
				"_id,a.0,a.a\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.0' and 'a.a' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Document field should throw an error if value has already been set as array (deep object)",
			nestedFieldsTestHelper(
				"_id,a.a.a.0,a.a.a.a\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a.a.0' and 'a.a.a.a' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Array field should throw an error if value has already been set as value",
			nestedFieldsTestHelper(
				"_id,a,a.0\n1,2,3",
				nil,
				fmt.Errorf("fields 'a' and 'a.0' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Array field should throw an error if value has already been set as value (deep object)",
			nestedFieldsTestHelper(
				"_id,a.a.a,a.a.a.0\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a.a' and 'a.a.a.0' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Array field should be incompatible with a document field starting with a symbol that is sorted before 0",
			nestedFieldsTestHelper(
				"_id,a./,a.0\n1,2,3",
				nil,
				fmt.Errorf("fields 'a./' and 'a.0' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Indexes in fields must start from 0",
			nestedFieldsTestHelper(
				"_id,a,b.1\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'b.1': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Indexes in fields must start from 0 (last field same length)",
			nestedFieldsTestHelper(
				"_id,a.b,b.1\n1,2,3",
				nil,
				fmt.Errorf("array index error with field 'b.1': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Fields that are the same should throw an error (no arrays)",
			nestedFieldsTestHelper(
				"_id,a.b,a.b\n1,2,3",
				nil,
				fmt.Errorf("fields cannot be identical: 'a.b' and 'a.b'"),
			),
		)
		Convey("With --useArrayIndexFields: Repeated array index should throw error",
			nestedFieldsTestHelper(
				"_id,a.0,a.1,a.2,a.0\n1,2,3,4,5",
				nil,
				fmt.Errorf("array index error with field 'a.0': array indexes in fields must start from 0 and increase sequentially"),
			),
		)
		Convey("With --useArrayIndexFields: Array entries of different types should throw an error",
			nestedFieldsTestHelper(
				"_id,a.a.0.a,a.a.0.1\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a.0.a' and 'a.a.0.1' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Document field should throw an error if element has already been set to an array",
			nestedFieldsTestHelper(
				"_id,a.0.0,a.0.a\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.0.0' and 'a.0.a' are incompatible"),
			),
		)
		Convey("With --useArrayIndexFields: Incompatible fields should throw error (one long, one short)",
			nestedFieldsTestHelper(
				"_id,a.a.a.a,a.a\n1,2,3",
				nil,
				fmt.Errorf("fields 'a.a.a.a' and 'a.a' are incompatible"),
			),
		)
	})
}

func nestedFieldsTestHelper(data string, expectedDocuments []bson.M, expectedErr error) func() {
	return func() {
		err := ioutil.WriteFile(util.ToUniversalPath("./temp_test_data.csv"), []byte(data), 0644)
		So(err, ShouldBeNil)
		defer func() {
			err = os.Remove(util.ToUniversalPath("./temp_test_data.csv"))
			So(err, ShouldBeNil)
		}()

		imp, err := NewMongoImport()
		So(err, ShouldBeNil)

		imp.InputOptions.Type = CSV
		imp.InputOptions.File = "./temp_test_data.csv"
		imp.InputOptions.HeaderLine = true
		imp.InputOptions.UseArrayIndexFields = true
		imp.IngestOptions.Mode = modeInsert

		numImported, numFailed, err := imp.ImportDocuments()
		if expectedDocuments == nil {
			So(err, ShouldNotBeNil)
			So(err, ShouldResemble, expectedErr)
		} else {
			So(err, ShouldBeNil)
			So(numImported, ShouldEqual, len(expectedDocuments))
			So(numFailed, ShouldEqual, 0)

			So(checkOnlyHasDocuments(imp.SessionProvider, expectedDocuments), ShouldBeNil)
		}
	}
}

// Regression test for TOOLS-1694 to prevent issue from TOOLS-1115
func TestHiddenOptionsDefaults(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.UnitTestType)
	Convey("With a new mongoimport with empty options", t, func() {
		imp := NewMockMongoImport()
		imp.ToolOptions = options.New("", "", "", "", true, options.EnabledOptions{})
		Convey("Then parsing should fill args with expected defaults", func() {
			_, err := imp.ToolOptions.ParseArgs([]string{})
			So(err, ShouldBeNil)

			// collection cannot be empty in validate
			imp.ToolOptions.Collection = "col"
			So(imp.validateSettings([]string{}), ShouldBeNil)
			So(imp.IngestOptions.NumDecodingWorkers, ShouldEqual, runtime.NumCPU())
			So(imp.IngestOptions.BulkBufferSize, ShouldEqual, 1000)
		})
	})
}

// generateTestData creates the files used in TestImportMIOSOE
func generateTestData() error {
	// If file exists already, don't both regenerating it.
	if _, err := os.Stat(mioSoeFile); err == nil {
		return nil
	}

	f, err := os.Create(mioSoeFile)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)

	// 10k unique _id's
	for i := 1; i < 10001; i++ {
		_, err = w.WriteString(fmt.Sprintf("{\"_id\": %v }\n", i))
		if err != nil {
			return err
		}
	}
	// 1 duplicate _id
	_, err = w.WriteString(fmt.Sprintf("{\"_id\": %v }\n", 5))
	if err != nil {
		return err
	}

	// 10k unique _id's
	for i := 10001; i < 20001; i++ {
		_, err = w.WriteString(fmt.Sprintf("{\"_id\": %v }\n", i))
		if err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}

	return nil
}

// test --maintainInsertionOrder and --stopOnError behavior
func TestImportMIOSOE(t *testing.T) {
	testtype.SkipUnlessTestType(t, testtype.IntegrationTestType)

	if err := generateTestData(); err != nil {
		t.Fatalf("Could not generate test data: %v", err)
	}

	client, err := testutil.GetBareSession()
	if err != nil {
		t.Fatalf("No server available")
	}
	database := client.Database("miodb")
	coll := database.Collection("mio")

	Convey("default restore ignores dup key errors", t, func() {
		imp, err := getImportWithArgs(mioSoeFile,
			"--collection", coll.Name(),
			"--db", database.Name(),
			"--drop")
		So(err, ShouldBeNil)
		So(imp.IngestOptions.MaintainInsertionOrder, ShouldBeFalse)

		nSuccess, nFailure, err := imp.ImportDocuments()
		So(err, ShouldBeNil)

		So(nSuccess, ShouldEqual, 20000)
		So(nFailure, ShouldEqual, 1)

		count, err := coll.CountDocuments(nil, bson.M{})
		So(err, ShouldBeNil)
		So(count, ShouldEqual, 20000)
	})

	Convey("--maintainInsertionOrder stops exactly on dup key errors", t, func() {
		imp, err := getImportWithArgs(mioSoeFile,
			"--collection", coll.Name(),
			"--db", database.Name(),
			"--drop",
			"--maintainInsertionOrder")
		So(err, ShouldBeNil)
		So(imp.IngestOptions.MaintainInsertionOrder, ShouldBeTrue)
		So(imp.IngestOptions.NumInsertionWorkers, ShouldEqual, 1)

		nSuccess, nFailure, err := imp.ImportDocuments()
		So(err, ShouldNotBeNil)

		So(nSuccess, ShouldEqual, 10000)
		So(nFailure, ShouldEqual, 1)
		So(err, ShouldNotBeNil)

		count, err := coll.CountDocuments(nil, bson.M{})
		So(err, ShouldBeNil)
		So(count, ShouldEqual, 10000)
	})

	Convey("--stopOnError stops on dup key errors", t, func() {
		imp, err := getImportWithArgs(mioSoeFile,
			"--collection", coll.Name(),
			"--db", database.Name(),
			"--drop",
			"--stopOnError")
		So(err, ShouldBeNil)
		So(imp.IngestOptions.StopOnError, ShouldBeTrue)

		nSuccess, nFailure, err := imp.ImportDocuments()
		So(err, ShouldNotBeNil)

		So(nSuccess, ShouldAlmostEqual, 10000, imp.IngestOptions.BulkBufferSize)
		So(nFailure, ShouldEqual, 1)

		count, err := coll.CountDocuments(nil, bson.M{})
		So(err, ShouldBeNil)
		So(count, ShouldAlmostEqual, 10000, imp.IngestOptions.BulkBufferSize)
	})

	_ = database.Drop(nil)
}
