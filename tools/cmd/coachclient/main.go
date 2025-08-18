package main

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/baely/infra/tools/gen/squad/v1alpha1"
)

func main() {
	cc, err := grpc.NewClient("127.0.0.1:8086", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}

	client := squadv1alpha1.NewCoachServiceClient(cc)

	if _, err := client.Assemble(context.Background(), &squadv1alpha1.AssembleRequest{
		Repo:  "txn",
		Ref:   "d1a41721dbc78761b12a386576196c5de77316b2",
		Image: "txn-test",
		Tag:   squadv1alpha1.AssembleRequest_TAG_SHA,
	}); err != nil {
		panic(err)
	}

	if _, err = client.Start(context.Background(), &squadv1alpha1.StartRequest{
		Service: "github.com_baely_ip",
		Ref:     "21cebcfae651da32e349127ac15185bf0b80cf4f",
	}); err != nil {
		panic(err)
	}
}
