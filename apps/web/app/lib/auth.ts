import { betterAuth } from "better-auth";
import { organization } from "better-auth/plugins";

export const auth = betterAuth({
  emailAndPassword: {
    enabled: true,
  },
  plugins: [
    organization({
      allowUserToCreateOrganization: true,
    }),
  ],
});

/**
 * TenantMapper maps an authenticated Better-Auth Organization to a Kubernetes Namespace & Kueue Queue.
 */
export function mapOrgToK8sNamespace(orgSlug: string): { namespace: string; queue: string } {
  const safeSlug = orgSlug.toLowerCase().replace(/[^a-z0-9-]/g, "-");
  return {
    namespace: `hpc-tenant-${safeSlug}`,
    queue: `queue-${safeSlug}`,
  };
}
