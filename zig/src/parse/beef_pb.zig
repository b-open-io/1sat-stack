// Hand-written protobuf binding for proto/beef.proto
/// package onesat.beef
const std = @import("std");

const protobuf = @import("protobuf");
const fd = protobuf.fd;

pub const DataFormat = enum(u8) {
    RAW_TX = 0,
    RAW_TX_AND_BUMP_INDEX = 1,
    TXID_ONLY = 2,
};

pub const BeefTx = struct {
    txid: []const u8 = &.{},
    data_format: u32 = 0,
    raw_tx: []const u8 = &.{},
    bump_index: ?u32 = null,

    pub const _desc_table = .{
        .txid = fd(1, .{ .scalar = .bytes }),
        .data_format = fd(2, .{ .scalar = .uint32 }),
        .raw_tx = fd(3, .{ .scalar = .bytes }),
        .bump_index = fd(4, .{ .scalar = .uint32 }),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};

pub const PathElement = struct {
    offset: u64 = 0,
    hash: []const u8 = &.{},
    txid: bool = false,
    duplicate: bool = false,

    pub const _desc_table = .{
        .offset = fd(1, .{ .scalar = .uint64 }),
        .hash = fd(2, .{ .scalar = .bytes }),
        .txid = fd(3, .{ .scalar = .bool }),
        .duplicate = fd(4, .{ .scalar = .bool }),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};

pub const PathLevel = struct {
    elements: std.ArrayListUnmanaged(PathElement) = .empty,

    pub const _desc_table = .{
        .elements = fd(1, .{ .repeated = .submessage }),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};

pub const MerklePath = struct {
    block_height: u32 = 0,
    path: std.ArrayListUnmanaged(PathLevel) = .empty,

    pub const _desc_table = .{
        .block_height = fd(1, .{ .scalar = .uint32 }),
        .path = fd(2, .{ .repeated = .submessage }),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};

pub const Beef = struct {
    bumps: std.ArrayListUnmanaged(MerklePath) = .empty,
    transactions: std.ArrayListUnmanaged(BeefTx) = .empty,

    pub const _desc_table = .{
        .bumps = fd(1, .{ .repeated = .submessage }),
        .transactions = fd(2, .{ .repeated = .submessage }),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};

pub const AtomicBeef = struct {
    txid: []const u8 = &.{},
    beef: ?Beef = null,

    pub const _desc_table = .{
        .txid = fd(1, .{ .scalar = .bytes }),
        .beef = fd(2, .submessage),
    };

    pub fn encode(self: @This(), writer: *std.Io.Writer, allocator: std.mem.Allocator) (std.Io.Writer.Error || std.mem.Allocator.Error)!void {
        return protobuf.encode(writer, allocator, self);
    }

    pub fn decode(reader: *std.Io.Reader, allocator: std.mem.Allocator) (protobuf.DecodingError || std.Io.Reader.Error || std.mem.Allocator.Error)!@This() {
        return protobuf.decode(@This(), reader, allocator);
    }

    pub fn deinit(self: *@This(), allocator: std.mem.Allocator) void {
        return protobuf.deinit(allocator, self);
    }
};
