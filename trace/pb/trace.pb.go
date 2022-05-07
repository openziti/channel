//
//Copyright 2019 NetFoundry, Inc.
//
//Licensed under the Apache License, Version 2.0 (the "License");
//you may not use this file except in compliance with the License.
//You may obtain a copy of the License at
//
//https://www.apache.org/licenses/LICENSE-2.0
//
//Unless required by applicable law or agreed to in writing, software
//distributed under the License is distributed on an "AS IS" BASIS,
//WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//See the License for the specific language governing permissions and
//limitations under the License.

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.27.1
// 	protoc        v3.19.1
// source: trace.proto

package trace_pb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type MessageType int32

const (
	MessageType_ChannelStateType   MessageType = 0
	MessageType_ChannelMessageType MessageType = 1
)

// Enum value maps for MessageType.
var (
	MessageType_name = map[int32]string{
		0: "ChannelStateType",
		1: "ChannelMessageType",
	}
	MessageType_value = map[string]int32{
		"ChannelStateType":   0,
		"ChannelMessageType": 1,
	}
)

func (x MessageType) Enum() *MessageType {
	p := new(MessageType)
	*p = x
	return p
}

func (x MessageType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (MessageType) Descriptor() protoreflect.EnumDescriptor {
	return file_trace_proto_enumTypes[0].Descriptor()
}

func (MessageType) Type() protoreflect.EnumType {
	return &file_trace_proto_enumTypes[0]
}

func (x MessageType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use MessageType.Descriptor instead.
func (MessageType) EnumDescriptor() ([]byte, []int) {
	return file_trace_proto_rawDescGZIP(), []int{0}
}

type TraceToggleVerbosity int32

const (
	TraceToggleVerbosity_ReportNone    TraceToggleVerbosity = 0
	TraceToggleVerbosity_ReportMatches TraceToggleVerbosity = 1
	TraceToggleVerbosity_ReportMisses  TraceToggleVerbosity = 2
	TraceToggleVerbosity_ReportAll     TraceToggleVerbosity = 3
)

// Enum value maps for TraceToggleVerbosity.
var (
	TraceToggleVerbosity_name = map[int32]string{
		0: "ReportNone",
		1: "ReportMatches",
		2: "ReportMisses",
		3: "ReportAll",
	}
	TraceToggleVerbosity_value = map[string]int32{
		"ReportNone":    0,
		"ReportMatches": 1,
		"ReportMisses":  2,
		"ReportAll":     3,
	}
)

func (x TraceToggleVerbosity) Enum() *TraceToggleVerbosity {
	p := new(TraceToggleVerbosity)
	*p = x
	return p
}

func (x TraceToggleVerbosity) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (TraceToggleVerbosity) Descriptor() protoreflect.EnumDescriptor {
	return file_trace_proto_enumTypes[1].Descriptor()
}

func (TraceToggleVerbosity) Type() protoreflect.EnumType {
	return &file_trace_proto_enumTypes[1]
}

func (x TraceToggleVerbosity) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use TraceToggleVerbosity.Descriptor instead.
func (TraceToggleVerbosity) EnumDescriptor() ([]byte, []int) {
	return file_trace_proto_rawDescGZIP(), []int{1}
}

type ChannelState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Timestamp     int64  `protobuf:"varint,1,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	Identity      string `protobuf:"bytes,2,opt,name=identity,proto3" json:"identity,omitempty"`
	Channel       string `protobuf:"bytes,3,opt,name=channel,proto3" json:"channel,omitempty"`
	Connected     bool   `protobuf:"varint,4,opt,name=connected,proto3" json:"connected,omitempty"`
	RemoteAddress string `protobuf:"bytes,5,opt,name=remoteAddress,proto3" json:"remoteAddress,omitempty"`
}

func (x *ChannelState) Reset() {
	*x = ChannelState{}
	if protoimpl.UnsafeEnabled {
		mi := &file_trace_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ChannelState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ChannelState) ProtoMessage() {}

func (x *ChannelState) ProtoReflect() protoreflect.Message {
	mi := &file_trace_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ChannelState.ProtoReflect.Descriptor instead.
func (*ChannelState) Descriptor() ([]byte, []int) {
	return file_trace_proto_rawDescGZIP(), []int{0}
}

func (x *ChannelState) GetTimestamp() int64 {
	if x != nil {
		return x.Timestamp
	}
	return 0
}

func (x *ChannelState) GetIdentity() string {
	if x != nil {
		return x.Identity
	}
	return ""
}

func (x *ChannelState) GetChannel() string {
	if x != nil {
		return x.Channel
	}
	return ""
}

func (x *ChannelState) GetConnected() bool {
	if x != nil {
		return x.Connected
	}
	return false
}

func (x *ChannelState) GetRemoteAddress() string {
	if x != nil {
		return x.RemoteAddress
	}
	return ""
}

type ChannelMessage struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Timestamp   int64  `protobuf:"varint,1,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	Identity    string `protobuf:"bytes,2,opt,name=identity,proto3" json:"identity,omitempty"`
	Channel     string `protobuf:"bytes,3,opt,name=channel,proto3" json:"channel,omitempty"`
	IsRx        bool   `protobuf:"varint,4,opt,name=isRx,proto3" json:"isRx,omitempty"`
	ContentType int32  `protobuf:"varint,5,opt,name=contentType,proto3" json:"contentType,omitempty"`
	Sequence    int32  `protobuf:"varint,6,opt,name=sequence,proto3" json:"sequence,omitempty"`
	ReplyFor    int32  `protobuf:"varint,7,opt,name=replyFor,proto3" json:"replyFor,omitempty"`
	Length      int32  `protobuf:"varint,8,opt,name=length,proto3" json:"length,omitempty"`
	Decode      []byte `protobuf:"bytes,9,opt,name=decode,proto3" json:"decode,omitempty"`
}

func (x *ChannelMessage) Reset() {
	*x = ChannelMessage{}
	if protoimpl.UnsafeEnabled {
		mi := &file_trace_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ChannelMessage) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ChannelMessage) ProtoMessage() {}

func (x *ChannelMessage) ProtoReflect() protoreflect.Message {
	mi := &file_trace_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ChannelMessage.ProtoReflect.Descriptor instead.
func (*ChannelMessage) Descriptor() ([]byte, []int) {
	return file_trace_proto_rawDescGZIP(), []int{1}
}

func (x *ChannelMessage) GetTimestamp() int64 {
	if x != nil {
		return x.Timestamp
	}
	return 0
}

func (x *ChannelMessage) GetIdentity() string {
	if x != nil {
		return x.Identity
	}
	return ""
}

func (x *ChannelMessage) GetChannel() string {
	if x != nil {
		return x.Channel
	}
	return ""
}

func (x *ChannelMessage) GetIsRx() bool {
	if x != nil {
		return x.IsRx
	}
	return false
}

func (x *ChannelMessage) GetContentType() int32 {
	if x != nil {
		return x.ContentType
	}
	return 0
}

func (x *ChannelMessage) GetSequence() int32 {
	if x != nil {
		return x.Sequence
	}
	return 0
}

func (x *ChannelMessage) GetReplyFor() int32 {
	if x != nil {
		return x.ReplyFor
	}
	return 0
}

func (x *ChannelMessage) GetLength() int32 {
	if x != nil {
		return x.Length
	}
	return 0
}

func (x *ChannelMessage) GetDecode() []byte {
	if x != nil {
		return x.Decode
	}
	return nil
}

type TogglePipeTracesRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Enable    bool                 `protobuf:"varint,1,opt,name=enable,proto3" json:"enable,omitempty"`
	Verbosity TraceToggleVerbosity `protobuf:"varint,2,opt,name=verbosity,proto3,enum=ziti.trace.pb.TraceToggleVerbosity" json:"verbosity,omitempty"`
	AppRegex  string               `protobuf:"bytes,3,opt,name=appRegex,proto3" json:"appRegex,omitempty"`
	PipeRegex string               `protobuf:"bytes,4,opt,name=pipeRegex,proto3" json:"pipeRegex,omitempty"`
}

func (x *TogglePipeTracesRequest) Reset() {
	*x = TogglePipeTracesRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_trace_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TogglePipeTracesRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TogglePipeTracesRequest) ProtoMessage() {}

func (x *TogglePipeTracesRequest) ProtoReflect() protoreflect.Message {
	mi := &file_trace_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TogglePipeTracesRequest.ProtoReflect.Descriptor instead.
func (*TogglePipeTracesRequest) Descriptor() ([]byte, []int) {
	return file_trace_proto_rawDescGZIP(), []int{2}
}

func (x *TogglePipeTracesRequest) GetEnable() bool {
	if x != nil {
		return x.Enable
	}
	return false
}

func (x *TogglePipeTracesRequest) GetVerbosity() TraceToggleVerbosity {
	if x != nil {
		return x.Verbosity
	}
	return TraceToggleVerbosity_ReportNone
}

func (x *TogglePipeTracesRequest) GetAppRegex() string {
	if x != nil {
		return x.AppRegex
	}
	return ""
}

func (x *TogglePipeTracesRequest) GetPipeRegex() string {
	if x != nil {
		return x.PipeRegex
	}
	return ""
}

var File_trace_proto protoreflect.FileDescriptor

var file_trace_proto_rawDesc = []byte{
	0x0a, 0x0b, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0d, 0x7a,
	0x69, 0x74, 0x69, 0x2e, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2e, 0x70, 0x62, 0x22, 0xa6, 0x01, 0x0a,
	0x0c, 0x43, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x1c, 0x0a,
	0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03,
	0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x12, 0x1a, 0x0a, 0x08, 0x69,
	0x64, 0x65, 0x6e, 0x74, 0x69, 0x74, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x69,
	0x64, 0x65, 0x6e, 0x74, 0x69, 0x74, 0x79, 0x12, 0x18, 0x0a, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e,
	0x65, 0x6c, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65,
	0x6c, 0x12, 0x1c, 0x0a, 0x09, 0x63, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x65, 0x64, 0x18, 0x04,
	0x20, 0x01, 0x28, 0x08, 0x52, 0x09, 0x63, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x65, 0x64, 0x12,
	0x24, 0x0a, 0x0d, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0d, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x41, 0x64,
	0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0x82, 0x02, 0x0a, 0x0e, 0x43, 0x68, 0x61, 0x6e, 0x6e, 0x65,
	0x6c, 0x4d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x12, 0x1c, 0x0a, 0x09, 0x74, 0x69, 0x6d, 0x65,
	0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x09, 0x74, 0x69, 0x6d,
	0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x12, 0x1a, 0x0a, 0x08, 0x69, 0x64, 0x65, 0x6e, 0x74, 0x69,
	0x74, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x08, 0x69, 0x64, 0x65, 0x6e, 0x74, 0x69,
	0x74, 0x79, 0x12, 0x18, 0x0a, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x07, 0x63, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x12, 0x12, 0x0a, 0x04,
	0x69, 0x73, 0x52, 0x78, 0x18, 0x04, 0x20, 0x01, 0x28, 0x08, 0x52, 0x04, 0x69, 0x73, 0x52, 0x78,
	0x12, 0x20, 0x0a, 0x0b, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x54, 0x79, 0x70, 0x65, 0x18,
	0x05, 0x20, 0x01, 0x28, 0x05, 0x52, 0x0b, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x54, 0x79,
	0x70, 0x65, 0x12, 0x1a, 0x0a, 0x08, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x18, 0x06,
	0x20, 0x01, 0x28, 0x05, 0x52, 0x08, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x12, 0x1a,
	0x0a, 0x08, 0x72, 0x65, 0x70, 0x6c, 0x79, 0x46, 0x6f, 0x72, 0x18, 0x07, 0x20, 0x01, 0x28, 0x05,
	0x52, 0x08, 0x72, 0x65, 0x70, 0x6c, 0x79, 0x46, 0x6f, 0x72, 0x12, 0x16, 0x0a, 0x06, 0x6c, 0x65,
	0x6e, 0x67, 0x74, 0x68, 0x18, 0x08, 0x20, 0x01, 0x28, 0x05, 0x52, 0x06, 0x6c, 0x65, 0x6e, 0x67,
	0x74, 0x68, 0x12, 0x16, 0x0a, 0x06, 0x64, 0x65, 0x63, 0x6f, 0x64, 0x65, 0x18, 0x09, 0x20, 0x01,
	0x28, 0x0c, 0x52, 0x06, 0x64, 0x65, 0x63, 0x6f, 0x64, 0x65, 0x22, 0xae, 0x01, 0x0a, 0x17, 0x54,
	0x6f, 0x67, 0x67, 0x6c, 0x65, 0x50, 0x69, 0x70, 0x65, 0x54, 0x72, 0x61, 0x63, 0x65, 0x73, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x65, 0x6e, 0x61, 0x62, 0x6c, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x08, 0x52, 0x06, 0x65, 0x6e, 0x61, 0x62, 0x6c, 0x65, 0x12, 0x41,
	0x0a, 0x09, 0x76, 0x65, 0x72, 0x62, 0x6f, 0x73, 0x69, 0x74, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0e, 0x32, 0x23, 0x2e, 0x7a, 0x69, 0x74, 0x69, 0x2e, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2e, 0x70,
	0x62, 0x2e, 0x54, 0x72, 0x61, 0x63, 0x65, 0x54, 0x6f, 0x67, 0x67, 0x6c, 0x65, 0x56, 0x65, 0x72,
	0x62, 0x6f, 0x73, 0x69, 0x74, 0x79, 0x52, 0x09, 0x76, 0x65, 0x72, 0x62, 0x6f, 0x73, 0x69, 0x74,
	0x79, 0x12, 0x1a, 0x0a, 0x08, 0x61, 0x70, 0x70, 0x52, 0x65, 0x67, 0x65, 0x78, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x08, 0x61, 0x70, 0x70, 0x52, 0x65, 0x67, 0x65, 0x78, 0x12, 0x1c, 0x0a,
	0x09, 0x70, 0x69, 0x70, 0x65, 0x52, 0x65, 0x67, 0x65, 0x78, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x09, 0x70, 0x69, 0x70, 0x65, 0x52, 0x65, 0x67, 0x65, 0x78, 0x2a, 0x3b, 0x0a, 0x0b, 0x4d,
	0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x54, 0x79, 0x70, 0x65, 0x12, 0x14, 0x0a, 0x10, 0x43, 0x68,
	0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x53, 0x74, 0x61, 0x74, 0x65, 0x54, 0x79, 0x70, 0x65, 0x10, 0x00,
	0x12, 0x16, 0x0a, 0x12, 0x43, 0x68, 0x61, 0x6e, 0x6e, 0x65, 0x6c, 0x4d, 0x65, 0x73, 0x73, 0x61,
	0x67, 0x65, 0x54, 0x79, 0x70, 0x65, 0x10, 0x01, 0x2a, 0x5a, 0x0a, 0x14, 0x54, 0x72, 0x61, 0x63,
	0x65, 0x54, 0x6f, 0x67, 0x67, 0x6c, 0x65, 0x56, 0x65, 0x72, 0x62, 0x6f, 0x73, 0x69, 0x74, 0x79,
	0x12, 0x0e, 0x0a, 0x0a, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x4e, 0x6f, 0x6e, 0x65, 0x10, 0x00,
	0x12, 0x11, 0x0a, 0x0d, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x4d, 0x61, 0x74, 0x63, 0x68, 0x65,
	0x73, 0x10, 0x01, 0x12, 0x10, 0x0a, 0x0c, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x4d, 0x69, 0x73,
	0x73, 0x65, 0x73, 0x10, 0x02, 0x12, 0x0d, 0x0a, 0x09, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74, 0x41,
	0x6c, 0x6c, 0x10, 0x03, 0x42, 0x2f, 0x5a, 0x2d, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x6f, 0x70, 0x65, 0x6e, 0x7a, 0x69, 0x74, 0x69, 0x2f, 0x63, 0x68, 0x61, 0x6e,
	0x6e, 0x65, 0x6c, 0x2f, 0x74, 0x72, 0x61, 0x63, 0x65, 0x2f, 0x70, 0x62, 0x2f, 0x74, 0x72, 0x61,
	0x63, 0x65, 0x5f, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_trace_proto_rawDescOnce sync.Once
	file_trace_proto_rawDescData = file_trace_proto_rawDesc
)

func file_trace_proto_rawDescGZIP() []byte {
	file_trace_proto_rawDescOnce.Do(func() {
		file_trace_proto_rawDescData = protoimpl.X.CompressGZIP(file_trace_proto_rawDescData)
	})
	return file_trace_proto_rawDescData
}

var file_trace_proto_enumTypes = make([]protoimpl.EnumInfo, 2)
var file_trace_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_trace_proto_goTypes = []interface{}{
	(MessageType)(0),                // 0: ziti.trace.pb.MessageType
	(TraceToggleVerbosity)(0),       // 1: ziti.trace.pb.TraceToggleVerbosity
	(*ChannelState)(nil),            // 2: ziti.trace.pb.ChannelState
	(*ChannelMessage)(nil),          // 3: ziti.trace.pb.ChannelMessage
	(*TogglePipeTracesRequest)(nil), // 4: ziti.trace.pb.TogglePipeTracesRequest
}
var file_trace_proto_depIdxs = []int32{
	1, // 0: ziti.trace.pb.TogglePipeTracesRequest.verbosity:type_name -> ziti.trace.pb.TraceToggleVerbosity
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_trace_proto_init() }
func file_trace_proto_init() {
	if File_trace_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_trace_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ChannelState); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_trace_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ChannelMessage); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_trace_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TogglePipeTracesRequest); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_trace_proto_rawDesc,
			NumEnums:      2,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_trace_proto_goTypes,
		DependencyIndexes: file_trace_proto_depIdxs,
		EnumInfos:         file_trace_proto_enumTypes,
		MessageInfos:      file_trace_proto_msgTypes,
	}.Build()
	File_trace_proto = out.File
	file_trace_proto_rawDesc = nil
	file_trace_proto_goTypes = nil
	file_trace_proto_depIdxs = nil
}
