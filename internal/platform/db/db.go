package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

type DB struct {
	Client   *mongo.Client
	Database *mongo.Database
}

func Connect(ctx context.Context, uri, name string) (*DB, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx, readpref.Primary()); err != nil {
		return nil, fmt.Errorf("mongo ping: %w", err)
	}
	return &DB{Client: client, Database: client.Database(name)}, nil
}

func (d *DB) C(name string) *mongo.Collection {
	return d.Database.Collection(name)
}

func (d *DB) Close(ctx context.Context) {
	_ = d.Client.Disconnect(ctx)
}
