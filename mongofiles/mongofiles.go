// Copyright (C) MongoDB, Inc. 2014-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package mongofiles provides an interface to GridFS collections in a MongoDB instance.
package mongofiles

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/huimingz/mongo-tools/common/db"
	"github.com/huimingz/mongo-tools/common/log"
	"github.com/huimingz/mongo-tools/common/options"
	"github.com/huimingz/mongo-tools/common/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/gridfs"
	driverOptions "go.mongodb.org/mongo-driver/mongo/options"
)

// List of possible commands for mongofiles.
const (
	List     = "list"
	Search   = "search"
	Put      = "put"
	PutID    = "put_id"
	Get      = "get"
	GetID    = "get_id"
	GetRegex = "get_regex"
	Delete   = "delete"
	DeleteID = "delete_id"
)

// MongoFiles is a container for the user-specified options and
// internal state used for running mongofiles.
type MongoFiles struct {
	// generic mongo tool options
	ToolOptions *options.ToolOptions

	// mongofiles-specific storage options
	StorageOptions *StorageOptions

	// mongofiles-specific input options
	InputOptions *InputOptions

	// for connecting to the db
	SessionProvider *db.SessionProvider

	// command to run
	Command string

	// filename in GridFS
	FileName string

	// ID to put into GridFS
	Id string

	// List of filenames for use as supporting
	// arguments in put and get commands
	FileNameList []string

	// Regular expression as supporting argument
	// for get_regex
	FileNameRegex string

	// GridFS bucket to operate on
	bucket *gridfs.Bucket
}

// New constructs a new mongofiles instance from the provided options. Will fail if cannot connect to server or if the
// provided options are invalid.
func New(opts Options) (*MongoFiles, error) {
	// create a session provider to connect to the db
	provider, err := db.NewSessionProvider(*opts.ToolOptions)
	if err != nil {
		return nil, util.SetupError{Err: fmt.Errorf("error connecting to host: %v", err)}
	}

	mf := &MongoFiles{
		ToolOptions:     opts.ToolOptions,
		StorageOptions:  opts.StorageOptions,
		SessionProvider: provider,
		InputOptions:    opts.InputOptions,
	}

	if err := mf.ValidateCommand(opts.ParsedArgs); err != nil {
		return nil, util.SetupError{Err: err, Message: util.ShortUsage("mongofiles")}
	}

	return mf, nil
}

// Close disconnects from the server and cleans up internal mongofiles state.
func (mf *MongoFiles) Close() {
	mf.SessionProvider.Close()
}

// ValidateCommand ensures the arguments supplied are valid.
func (mf *MongoFiles) ValidateCommand(args []string) error {
	// make sure a command is specified and that we don't have
	// too many arguments
	if len(args) == 0 {
		return fmt.Errorf("no command specified")
	}

	switch args[0] {
	case List:
		if len(args) > 2 {
			return fmt.Errorf("too many non-URI positional arguments (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)")
		}
		if len(args) == 1 {
			mf.FileName = ""
		} else {
			mf.FileName = args[1]
		}
	case Put, Get:
		// monogofiles put ... and mongofiles get ... should work
		// over a list of files, i.e. by using mf.FileNameList
		if len(args) == 1 || args[1] == "" {
			return fmt.Errorf("'%v' argument missing", args[0])
		}

		mf.FileNameList = args[1:]
	case GetRegex:
		// mongofiles get_regex ... should work over a PCRE
		// and a string of options passed to the $regex query
		if len(args) == 1 || args[1] == "" {
			return fmt.Errorf("'%v' argument missing", args[0])
		} else if len(args) > 2 {
			return fmt.Errorf("too many non-URI positional arguments (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)")
		}

		mf.FileNameRegex = args[1]
	case Search, Delete:
		if len(args) > 2 {
			return fmt.Errorf("too many non-URI positional arguments (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)")
		}
		// also make sure the supporting argument isn't literally an
		// empty string for example, mongofiles get ""
		if len(args) == 1 || args[1] == "" {
			return fmt.Errorf("'%v' argument missing", args[0])
		}
		mf.FileName = args[1]
	case GetID, DeleteID:
		if len(args) > 2 {
			return fmt.Errorf("too many non-URI positional arguments (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)")
		}
		if len(args) == 1 || args[1] == "" {
			return fmt.Errorf("'%v' argument missing", args[0])
		}
		mf.Id = args[1]
	case PutID:
		if len(args) > 3 {
			return fmt.Errorf("too many non-URI positional arguments (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)")
		}
		if len(args) < 3 || args[1] == "" || args[2] == "" {
			return fmt.Errorf("'%v' argument(s) missing", args[0])
		}
		mf.FileName = args[1]
		mf.Id = args[2]
	default:
		return fmt.Errorf("'%v' is not a valid command (If you are trying to specify a connection string, it must begin with mongodb:// or mongodb+srv://)", args[0])
	}

	if mf.StorageOptions.GridFSPrefix == "" {
		return fmt.Errorf("--prefix can not be blank")
	}

	mf.Command = args[0]
	return nil
}

// Query GridFS for files and display the results.
func (mf *MongoFiles) findAndDisplay(query bson.M) (string, error) {
	gridFiles, err := mf.findGFSFiles(query)
	if err != nil {
		return "", fmt.Errorf("error retrieving list of GridFS files: %v", err)
	}

	var display string
	for _, gridFile := range gridFiles {
		display += fmt.Sprintf("%s\t%d\n", gridFile.Name, gridFile.Length)
	}

	return display, nil
}

// Return the local filename, as specified by the --local flag. Defaults to
// the GridFile's name if not present. If GridFile is nil, uses the filename
// given on the command line.
func (mf *MongoFiles) getLocalFileName(gridFile *gfsFile) string {
	localFileName := mf.StorageOptions.LocalFileName
	if localFileName == "" {
		if gridFile != nil {
			localFileName = gridFile.Name
		} else {
			localFileName = mf.FileName
		}
	}
	return localFileName
}

// handleGet contains the logic for the 'get' and 'get_id' commands
func (mf *MongoFiles) handleGet() (err error) {
	files, err := mf.getTargetGFSFiles()
	if err != nil {
		return err
	}

	if len(files) > 1 && mf.StorageOptions.LocalFileName != "" {
		return fmt.Errorf("cannot get multiple files with --local specified")
	}

	for _, file := range files {
		if err = mf.writeGFSFileToLocal(file); err != nil {
			return err
		}
	}

	return nil
}

// Gets all GridFS files that match the given query.
func (mf *MongoFiles) findGFSFiles(query bson.M) (files []*gfsFile, err error) {
	cursor, err := mf.bucket.Find(query)
	if err != nil {
		return nil, err
	}
	dc := util.DeferredCloser{Closer: &util.CloserCursor{Cursor: cursor}}
	defer dc.CloseWithErrorCapture(&err)

	for cursor.Next(context.Background()) {
		var out *gfsFile
		out, err = newGfsFileFromCursor(cursor, mf)
		if err != nil {
			return nil, err
		}
		files = append(files, out)
	}

	return files, nil
}

// Gets the GridFS file the options specify. Use this for the get family of commands.
func (mf *MongoFiles) getTargetGFSFiles() ([]*gfsFile, error) {
	var query bson.M
	var minimumExpectedDocs = 1
	var minimumExpectedDocsError error

	if len(mf.FileNameList) > 0 {
		// Case supporting queries one or many files specified in mongofiles ... get ...
		query = bson.M{"filename": bson.M{"$in": mf.FileNameList}}
		minimumExpectedDocs = len(mf.FileNameList)
		minimumExpectedDocsError = fmt.Errorf("requested files not found: %v", mf.FileNameList)
	} else if mf.FileNameRegex != "" {
		// Case supporting queries by regex specified in mongofiles ... get_regex ...
		query = bson.M{
			"filename": bson.M{
				"$regex":   mf.FileNameRegex,
				"$options": mf.StorageOptions.RegexOptions,
			},
		}
		minimumExpectedDocsError = fmt.Errorf("files matching the following pattern were not found: %v", mf.FileNameRegex)
	} else if mf.Id != "" {
		// Case supporting queries by file ID specified in mongofiles ... get_id ...
		minimumExpectedDocsError = fmt.Errorf("no such file with _id: %v", mf.Id)

		id, err := mf.parseOrCreateID()
		if err != nil {
			return nil, err
		}

		query = bson.M{"_id": id}
	} else {
		// Case supporting queries of a single file with specific local
		// file name, i.e. with mongofiles ... get ... --local ...
		query = bson.M{"filename": mf.FileName}
		minimumExpectedDocsError = fmt.Errorf("no such file with name: %v", mf.FileName)
	}

	gridFiles, err := mf.findGFSFiles(query)
	if err != nil {
		return nil, err
	}

	if len(gridFiles) < minimumExpectedDocs {
		return nil, minimumExpectedDocsError
	}

	return gridFiles, nil
}

// Delete all files with the given filename.
func (mf *MongoFiles) deleteAll(filename string) error {
	gridFiles, err := mf.findGFSFiles(bson.M{"filename": filename})
	if err != nil {
		return err
	}

	for _, gridFile := range gridFiles {
		if err := gridFile.Delete(); err != nil {
			return err
		}
	}
	log.Logvf(log.Always, "successfully deleted all instances of '%v' from GridFS\n", mf.FileName)

	return nil
}

// handleDeleteID contains the logic for the 'delete_id' command
func (mf *MongoFiles) handleDeleteID() error {
	files, err := mf.getTargetGFSFiles()
	if err != nil {
		return err
	}

	file := files[0]
	if err := file.Delete(); err != nil {
		return err
	}
	log.Logvf(log.Always, fmt.Sprintf("successfully deleted file with _id %v from GridFS", mf.Id))

	return nil
}

// parse and convert input extended JSON _id. Generates a new ObjectID if no _id provided.
func (mf *MongoFiles) parseOrCreateID() (interface{}, error) {
	trimmed := strings.Trim(mf.Id, " ")

	if trimmed == "" {
		return primitive.NewObjectID(), nil
	}

	// Wrap JSON bytes into a document for unmarshaling, then pick out the value after.
	var wrapped string
	switch trimmed[0] {
	case '{':
		wrapped = fmt.Sprintf(`{"_id":%s}`, trimmed)
	default:
		wrapped = fmt.Sprintf(`{"_id":"%s"}`, trimmed)
	}
	var idDoc bson.D
	err := bson.UnmarshalExtJSON([]byte(wrapped), false, &idDoc)
	if err != nil {
		return nil, fmt.Errorf("error parsing id as Extended JSON: %v", err)
	}

	return idDoc[0].Value, nil
}

// writeGFSFileToLocal writes a file from gridFS to stdout or the filesystem.
func (mf *MongoFiles) writeGFSFileToLocal(gridFile *gfsFile) (err error) {
	localFileName := mf.getLocalFileName(gridFile)
	var localFile io.WriteCloser
	if localFileName == "-" {
		localFile = os.Stdout
	} else {
		if localFile, err = os.Create(localFileName); err != nil {
			return fmt.Errorf("error while opening local file '%v': %v", localFileName, err)
		}
		dc := util.DeferredCloser{Closer: localFile}
		defer dc.CloseWithErrorCapture(&err)
		log.Logvf(log.DebugLow, "created local file '%v'", localFileName)
	}

	stream, err := gridFile.OpenStreamForReading()
	if err != nil {
		return err
	}
	dc := util.DeferredCloser{Closer: stream}
	defer dc.CloseWithErrorCapture(&err)

	if _, err = io.Copy(localFile, stream); err != nil {
		return fmt.Errorf("error while writing Data into local file '%v': %v", localFileName, err)
	}

	log.Logvf(log.Always, fmt.Sprintf("finished writing to %s\n", localFileName))
	return nil
}

// Write the given GridFS file to the database. Will fail if file already exists and --replace flag turned off.
func (mf *MongoFiles) put(id interface{}, name string) (bytesWritten int64, err error) {
	gridFile, err := newGfsFile(id, name, mf)
	if err != nil {
		return 0, err
	}

	localFileName := mf.getLocalFileName(gridFile)

	var localFile io.ReadCloser
	if localFileName == "-" {
		localFile = os.Stdin
	} else {
		localFile, err = os.Open(localFileName)
		if err != nil {
			return 0, fmt.Errorf("error while opening local gridFile '%v' : %v", localFileName, err)
		}
		dc := util.DeferredCloser{Closer: localFile}
		defer dc.CloseWithErrorCapture(&err)
		log.Logvf(log.DebugLow, "creating GridFS gridFile '%v' from local gridFile '%v'", mf.FileName, localFileName)
	}

	// check if --replace flag turned on
	if mf.StorageOptions.Replace {
		if err = mf.deleteAll(gridFile.Name); err != nil {
			return 0, err
		}
	}

	if mf.StorageOptions.ContentType != "" {
		gridFile.Metadata.ContentType = mf.StorageOptions.ContentType
	}

	stream, err := gridFile.OpenStreamForWriting()
	if err != nil {
		return 0, err
	}
	dc := util.DeferredCloser{Closer: stream}
	defer dc.CloseWithErrorCapture(&err)

	n, err := io.Copy(stream, localFile)
	if err != nil {
		return n, fmt.Errorf("error while storing '%v' into GridFS: %v", localFileName, err)
	}

	return n, nil
}

// handlePut contains the logic for the 'put' and 'put_id' commands
func (mf *MongoFiles) handlePut() error {
	if len(mf.FileNameList) == 0 {
		mf.FileNameList = []string{mf.FileName}
	}

	for _, filename := range mf.FileNameList {
		id, err := mf.parseOrCreateID()
		if err != nil {
			return err
		}

		log.Logvf(log.Always, "adding gridFile: %v\n", filename)

		n, err := mf.put(id, filename)
		if err != nil {
			log.Logvf(log.Always, "error adding gridFile: %v\n", err)
			return err
		}
		log.Logvf(log.DebugLow, "copied %v bytes to server", n)
		log.Logvf(log.Always, "added gridFile: %v\n", filename)
	}

	return nil
}

// Run the mongofiles utility. If displayHost is true, the connected host/port is
// displayed.
func (mf *MongoFiles) Run(displayHost bool) (output string, finalErr error) {
	var err error

	// check type of node we're connected to, and fall back to w=1 if standalone (for <= 2.4)
	nodeType, err := mf.SessionProvider.GetNodeType()
	if err != nil {
		return "", fmt.Errorf("error determining type of node connected: %v", err)
	}

	log.Logvf(log.DebugLow, "connected to node type: %v", nodeType)

	client, err := mf.SessionProvider.GetSession()
	if err != nil {
		return "", fmt.Errorf("error getting client: %v", err)
	}

	err = client.Ping(context.Background(), nil)
	if err != nil {
		return "", fmt.Errorf("error connecting to host: %v", err)
	}

	database := client.Database(mf.StorageOptions.DB)
	mf.bucket, err = gridfs.NewBucket(database, &driverOptions.BucketOptions{Name: &mf.StorageOptions.GridFSPrefix})
	if err != nil {
		return "", fmt.Errorf("error getting GridFS bucket: %v", err)
	}

	if displayHost {
		log.Logvf(log.Always, "connected to: %v", util.SanitizeURI(mf.ToolOptions.URI.ConnectionString))
	}

	// first validate the namespaces we'll be using: <db>.<prefix>.files and <db>.<prefix>.chunks
	// it's ok to validate only <db>.<prefix>.chunks (the longer one)
	err = util.ValidateFullNamespace(fmt.Sprintf("%s.%s.chunks", mf.StorageOptions.DB,
		mf.StorageOptions.GridFSPrefix))
	if err != nil {
		return "", err
	}

	log.Logvf(log.Info, "handling mongofiles '%v' command...", mf.Command)

	switch mf.Command {

	case List:
		query := bson.M{}
		if mf.FileName != "" {
			regex := bson.M{"$regex": "^" + regexp.QuoteMeta(mf.FileName)}
			query = bson.M{"filename": regex}
		}
		output, err = mf.findAndDisplay(query)

	case Search:
		regex := bson.M{"$regex": mf.FileName}
		query := bson.M{"filename": regex}

		output, err = mf.findAndDisplay(query)

	case Get, GetID, GetRegex:
		err = mf.handleGet()

	case Put, PutID:
		err = mf.handlePut()

	case DeleteID:
		err = mf.handleDeleteID()

	case Delete:
		err = mf.deleteAll(mf.FileName)
	}

	return output, err
}
