import Users from "../sections/Users";

export default function UsersPage() {
  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold">Users</h2>
        <p className="text-sm text-muted-foreground">Manage admin access</p>
      </div>
      <Users />
    </div>
  );
}
