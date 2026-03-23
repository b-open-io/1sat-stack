const std = @import("std");
const bsvz = @import("bsvz");

pub const Outpoint = struct {
    txid: [32]u8,
    vout: u32,

    pub fn ordinalString(self: *const Outpoint) ![72]u8 {
        var buf: [72]u8 = undefined;
        _ = std.fmt.bufPrint(&buf, "{s}_{d}", .{
            std.fmt.fmtSliceHexLower(&self.txid),
            self.vout,
        }) catch return buf;
        return buf;
    }
};

/// Accumulated results from all parsers for a single output.
pub const ParseContext = struct {
    allocator: std.mem.Allocator,
    outpoint: ?Outpoint,
    locking_script: []const u8,
    satoshis: u64,
    results: std.StringHashMapUnmanaged(ParseResult),

    pub fn init(
        allocator: std.mem.Allocator,
        locking_script: []const u8,
        satoshis: u64,
        outpoint: ?Outpoint,
    ) ParseContext {
        return .{
            .allocator = allocator,
            .outpoint = outpoint,
            .locking_script = locking_script,
            .satoshis = satoshis,
            .results = .empty,
        };
    }

    pub fn deinit(self: *ParseContext) void {
        var it = self.results.iterator();
        while (it.next()) |entry| {
            entry.value_ptr.deinit(self.allocator);
        }
        self.results.deinit(self.allocator);
    }

    pub fn addResult(self: *ParseContext, result: ParseResult) !void {
        try self.results.put(self.allocator, result.tag, result);
    }

    pub fn getResult(self: *const ParseContext, tag: []const u8) ?*const ParseResult {
        return self.results.getPtr(tag);
    }
};

/// Output of a single parser.
pub const ParseResult = struct {
    tag: []const u8,
    events: std.ArrayListUnmanaged([]const u8),
    owners: std.ArrayListUnmanaged([20]u8),

    /// Opaque data for parser chaining. Later parsers can inspect this.
    /// Each parser defines its own data type stored here.
    data: ?*anyopaque,

    pub fn init(tag: []const u8) ParseResult {
        return .{
            .tag = tag,
            .events = .empty,
            .owners = .empty,
            .data = null,
        };
    }

    pub fn addEvent(self: *ParseResult, allocator: std.mem.Allocator, event: []const u8) !void {
        try self.events.append(allocator, event);
    }

    pub fn addOwner(self: *ParseResult, allocator: std.mem.Allocator, pkhash: [20]u8) !void {
        try self.owners.append(allocator, pkhash);
    }

    pub fn deinit(self: *ParseResult, allocator: std.mem.Allocator) void {
        self.events.deinit(allocator);
        self.owners.deinit(allocator);
    }
};

/// Parser function signature. Each parser implements this.
pub const ParserFn = *const fn (ctx: *ParseContext) anyerror!?ParseResult;
