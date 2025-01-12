// Copyright (C) MongoDB, Inc. 2014-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package mongoimport

import (
	"fmt"

	"github.com/huimingz/mongo-tools/common/db"
	"github.com/huimingz/mongo-tools/common/log"
	"github.com/huimingz/mongo-tools/common/options"
)

var Usage = `<options> <connection-string> <file> 

Import CSV, TSV or JSON data into MongoDB. If no file is provided, mongoimport reads from stdin.

Connection strings must begin with mongodb:// or mongodb+srv://.

See http://docs.mongodb.com/database-tools/mongoimport/ for more information.`

// InputOptions defines the set of options for reading input data.
type InputOptions struct {
	// Fields is an option to directly specify comma-separated fields to import to CSV.
	Fields *string `long:"fields" value-name:"<field>[,<field>]*" short:"f" description:"comma separated list of fields, e.g. -f name,age"`

	// FieldFile is a filename that refers to a list of fields to import, 1 per line.
	FieldFile *string `long:"fieldFile" value-name:"<filename>" description:"file with field names - 1 per line"`

	// Specifies the location and name of a file containing the data to import.
	File string `long:"file" value-name:"<filename>" description:"file to import from; if not specified, stdin is used"`

	// Treats the input source's first line as field list (csv and tsv only).
	HeaderLine bool `long:"headerline" description:"use first line in input source as the field list (CSV and TSV only)"`

	// Indicates that the underlying input source contains a single JSON array with the documents to import.
	JSONArray bool `long:"jsonArray" description:"treat input source as a JSON array"`

	// Indicates how to handle type coercion failures
	ParseGrace string `long:"parseGrace" value-name:"<grace>" default:"stop" description:"controls behavior when type coercion fails - one of: autoCast, skipField, skipRow, stop"`

	// Specifies the file type to import. The default format is JSON, but it’s possible to import CSV and TSV files.
	Type string `long:"type" value-name:"<type>" default:"json" default-mask:"-" description:"input format to import: json, csv, or tsv"`

	// Indicates that field names include type descriptions
	ColumnsHaveTypes bool `long:"columnsHaveTypes" description:"indicates that the field list (from --fields, --fieldsFile, or --headerline) specifies types; They must be in the form of '<colName>.<type>(<arg>)'. The type can be one of: auto, binary, boolean, date, date_go, date_ms, date_oracle, decimal, double, int32, int64, string. For each of the date types, the argument is a datetime layout string. For the binary type, the argument can be one of: base32, base64, hex. All other types take an empty argument. Only valid for CSV and TSV imports. e.g. zipcode.string(), thumbnail.binary(base64)"`

	// Indicates that the legacy extended JSON format should be used to parse JSON documents. Defaults to false.
	Legacy bool `long:"legacy" description:"use the legacy extended JSON format"`

	UseArrayIndexFields bool `long:"useArrayIndexFields" description:"indicates that field names may include array indexes that should be used to construct arrays during import (e.g. foo.0,foo.1). Indexes must start from 0 and increase sequentially (foo.1,foo.0 would fail)."`
}

// Name returns a description of the InputOptions struct.
func (_ *InputOptions) Name() string {
	return "input"
}

// IngestOptions defines the set of options for storing data.
type IngestOptions struct {
	// Drops target collection before importing.
	Drop bool `long:"drop" description:"drop collection before inserting documents"`

	// Ignores fields with empty values in CSV and TSV imports.
	IgnoreBlanks bool `long:"ignoreBlanks" description:"ignore fields with empty values in CSV and TSV"`

	// Indicates that documents will be inserted in the order of their appearance in the input source.
	MaintainInsertionOrder bool `long:"maintainInsertionOrder" description:"insert the documents in the order of their appearance in the input source. By default the insertions will be performed in an arbitrary order. Setting this flag also enables the behavior of --stopOnError and restricts NumInsertionWorkers to 1."`

	// Sets the number of insertion routines to use
	NumInsertionWorkers int `short:"j" value-name:"<number>" long:"numInsertionWorkers" description:"number of insert operations to run concurrently" default:"1" default-mask:"-"`

	// Forces mongoimport to halt the import operation at the first insert or upsert error.
	StopOnError bool `long:"stopOnError" description:"halt after encountering any error during importing. By default, mongoimport will attempt to continue through document validation and DuplicateKey errors, but with this option enabled, the tool will stop instead. A small number of documents may be inserted after encountering an error even with this option enabled; use --maintainInsertionOrder to halt immediately after an error"`

	// Modify the import process.
	// For existing documents (match --upsertFields) in the database:
	// "insert": Insert only, skip existing documents.
	// "upsert": Insert new documents or replace existing ones.
	// "merge": Insert new documents or modify existing ones; Preserve values in the database that are not overwritten.
	// "delete": Skip new documents or delete existing ones that match --upsertFields.
	// We don't set `default: insert` here since we need to be able to set mode to upsert if --mode isn't set and --upsertFields is set.
	Mode string `long:"mode" choice:"insert" choice:"upsert" choice:"merge" choice:"delete" description:"insert: insert only, skips matching documents. upsert: insert new documents or replace existing documents. merge: insert new documents or modify existing documents. delete: deletes matching documents only. If upsert fields match more than one document, only one document is deleted. (default: insert)"`

	Upsert bool `long:"upsert" hidden:"true" description:"(deprecated; same as --mode=upsert) insert or update objects that already exist"`

	// Specifies a list of fields for the query portion of the upsert; defaults to _id field.
	UpsertFields string `long:"upsertFields" value-name:"<field>[,<field>]*" description:"comma-separated fields for the query part when --mode is set to upsert or merge"`

	// Sets write concern level for write operations.
	// By default mongoimport uses a write concern of 'majority'.
	// Cannot be used simultaneously with write concern options in a URI.
	WriteConcern string `long:"writeConcern" value-name:"<write-concern-specifier>" default-mask:"-" description:"write concern options e.g. --writeConcern majority, --writeConcern '{w: 3, wtimeout: 500, fsync: true, j: true}'"`

	// Indicates that the server should bypass document validation on import.
	BypassDocumentValidation bool `long:"bypassDocumentValidation" description:"bypass document validation"`

	// Specifies the number of threads to use in processing data read from the input source
	NumDecodingWorkers int `long:"numDecodingWorkers" default:"0" hidden:"true"`

	BulkBufferSize int `long:"batchSize" default:"1000" hidden:"true"`
}

// Name returns a description of the IngestOptions struct.
func (_ *IngestOptions) Name() string {
	return "ingest"
}

// Options contains all the possible options that can be used to configure mongoimport.
type Options struct {
	*options.ToolOptions
	*InputOptions
	*IngestOptions
	ParsedArgs []string
}

// ParseOptions reads command line arguments and converts them into options used to configure mongoimport.
func ParseOptions(rawArgs []string, versionStr, gitCommit string) (Options, error) {
	opts := options.New("mongoimport", versionStr, gitCommit, Usage, true,
		options.EnabledOptions{Auth: true, Connection: true, Namespace: true, URI: true})
	inputOpts := &InputOptions{}
	ingestOpts := &IngestOptions{}
	opts.AddOptions(inputOpts)
	opts.AddOptions(ingestOpts)

	extraArgs, err := opts.ParseArgs(rawArgs)
	if err != nil {
		return Options{}, err
	}

	if len(extraArgs) > 1 {
		return Options{}, fmt.Errorf("error parsing positional arguments: " +
			"provide only one file name and only one MongoDB connection string. " +
			"Connection strings must begin with mongodb:// or mongodb+srv:// schemes",
		)
	}

	log.SetVerbosity(opts.Verbosity)
	opts.URI.LogUnsupportedOptions()

	wc, err := db.NewMongoWriteConcern(ingestOpts.WriteConcern, opts.URI.ParsedConnString())
	if err != nil {
		return Options{}, fmt.Errorf("error constructing write concern: %v", err)
	}
	opts.WriteConcern = wc

	// ensure either a positional argument is supplied or an argument is passed
	// to the --file flag - and not both
	if inputOpts.File != "" && len(extraArgs) != 0 {
		return Options{}, fmt.Errorf("error parsing positional arguments: cannot use both --file and a positional argument to set the input file")
	}

	if inputOpts.File == "" {
		if len(extraArgs) != 0 {
			// if --file is not supplied, use the positional argument supplied
			inputOpts.File = extraArgs[0]
		}
	}

	return Options{
		opts,
		inputOpts,
		ingestOpts,
		extraArgs,
	}, nil
}
