package builders

import (
	"fmt"
)

type ProjectBuilder interface {
	GetDockerfile() string
}

type StaticBuilder struct{}

func (b *StaticBuilder) GetDockerfile() string {
	return "FROM nginx:alpine\nCOPY . /usr/share/nginx/html/\nEXPOSE 80\nCMD [\"nginx\", \"-g\", \"daemon off;\"]"
}

type ReactBuilder struct{}

func (b *ReactBuilder) GetDockerfile() string {
	return "FROM node:18-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install && npm run build\nRUN npm install -g serve\nEXPOSE 3000\nCMD [\"serve\", \"-s\", \"build\", \"-l\", \"3000\"]"
}

type NodeBuilder struct{}

func (b *NodeBuilder) GetDockerfile() string {
	return "FROM node:18-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install\nEXPOSE 8080\nCMD [\"npm\", \"start\"]"
}

type AstroBuilder struct{}

func (b *AstroBuilder) GetDockerfile() string {
	return "FROM node:18-alpine\nWORKDIR /app\nCOPY . .\nRUN npm install && npm run build\nFROM nginx:alpine\nCOPY --from=0 /app/dist /usr/share/nginx/html\nEXPOSE 80\nCMD [\"nginx\", \"-g\", \"daemon off;\"]"
}

type GoBuilder struct{}

func (b *GoBuilder) GetDockerfile() string {
	return "FROM golang:1.21-alpine\nWORKDIR /app\nCOPY . .\nRUN go build -o main .\nEXPOSE 8080\nCMD [\"./main\"]"
}

var builders = map[string]ProjectBuilder{
	"static": &StaticBuilder{},
	"react":  &ReactBuilder{},
	"node":   &NodeBuilder{},
	"astro":  &AstroBuilder{},
	"go":     &GoBuilder{},
}

func GetBuilder(projectType string) (ProjectBuilder, error) {
	builder, ok := builders[projectType]
	if !ok {
		return nil, fmt.Errorf("unsupported project type: %s", projectType)
	}
	return builder, nil
}
