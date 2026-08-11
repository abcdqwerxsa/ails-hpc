package e2e

import (
	"testing"
)

// Exported Runner Functions for execution from both test/e2e and pkg/services

func RunAllE2ETests(t *testing.T) {
	t.Run("Tier1_FeatureCoverage", RunTier1Tests)
	t.Run("Tier2_BoundaryCases", RunTier2Tests)
	t.Run("Tier3_Combinations", RunTier3Tests)
	t.Run("Tier4_Scenarios", RunTier4Tests)
}

func RunTier1Tests(t *testing.T) {
	t.Run("Jobs_SbatchSubmission", TestTier1_Jobs_SbatchSubmission)
	t.Run("Jobs_ScancelTermination", TestTier1_Jobs_ScancelTermination)
	t.Run("Jobs_SholdPause", TestTier1_Jobs_SholdPause)
	t.Run("Jobs_SrequeueRestart", TestTier1_Jobs_SrequeueRestart)
	t.Run("Jobs_ListQueue", TestTier1_Jobs_ListJobsQueue)
	t.Run("Jobs_PartitionAssignment", TestTier1_Jobs_PartitionAssignment)

	t.Run("Nodes_ListNodes", TestTier1_Nodes_ListNodes)
	t.Run("Nodes_DrainNode1", TestTier1_Nodes_DrainNode1)
	t.Run("Nodes_ResumeNode1", TestTier1_Nodes_ResumeNode1)
	t.Run("Nodes_DrainNode2", TestTier1_Nodes_DrainNode2)
	t.Run("Nodes_ResumeNode2", TestTier1_Nodes_ResumeNode2)
	t.Run("Nodes_GridVerification", TestTier1_Nodes_StateGridVerification)

	t.Run("Containers_LaunchVSCode", TestTier1_Containers_LaunchVSCode)
	t.Run("Containers_LaunchJupyterLab", TestTier1_Containers_LaunchJupyterLab)
	t.Run("Containers_JWTProxyValidation", TestTier1_Containers_JWTProxyValidation)
	t.Run("Containers_ListActive", TestTier1_Containers_ListActiveContainers)
	t.Run("Containers_RecycleInstance", TestTier1_Containers_RecycleContainerInstance)

	t.Run("Billing_UsageStats", TestTier1_Billing_QueryUsageStats)
	t.Run("Billing_UserFilter", TestTier1_Billing_QueryUserUsageFilter)
	t.Run("Billing_ProjectFilter", TestTier1_Billing_QueryProjectUsageFilter)
	t.Run("Billing_ExportJSON", TestTier1_Billing_ExportJSONReport)
	t.Run("Billing_ExportChart", TestTier1_Billing_ExportChartReport)
}

func RunTier2Tests(t *testing.T) {
	t.Run("Jobs_EmptySubmission", TestTier2_Jobs_EmptySubmissionPayload)
	t.Run("Jobs_InvalidCancelID", TestTier2_Jobs_InvalidJobIDCancel)
	t.Run("Jobs_HoldCancelledJob", TestTier2_Jobs_HoldCancelledJob)
	t.Run("Jobs_RequeueNonExistent", TestTier2_Jobs_RequeueNonExistentJob)
	t.Run("Jobs_ExcessiveCPUs", TestTier2_Jobs_ExcessiveCPUsRequest)

	t.Run("Nodes_NonExistentNode", TestTier2_Nodes_NonExistentNodeStateUpdate)
	t.Run("Nodes_InvalidStateValue", TestTier2_Nodes_InvalidStateValue)
	t.Run("Nodes_EmptyStatePayload", TestTier2_Nodes_EmptyStatePayload)
	t.Run("Nodes_RepeatDrain", TestTier2_Nodes_RepeatDrainStateIdempotency)
	t.Run("Nodes_NegativePath", TestTier2_Nodes_NegativeNodeIDPath)

	t.Run("Containers_UnsupportedEnv", TestTier2_Containers_UnsupportedEnvType)
	t.Run("Containers_NegativeResources", TestTier2_Containers_NegativeResources)
	t.Run("Containers_ExceedingQuota", TestTier2_Containers_ExceedingQuotaLimit)
	t.Run("Containers_RecycleNonExistent", TestTier2_Containers_RecycleNonExistentContainer)
	t.Run("Containers_RecycleAlreadyRecycled", TestTier2_Containers_RecycleAlreadyRecycledContainer)

	t.Run("Billing_InvalidDateRange", TestTier2_Billing_InvalidDateRange)
	t.Run("Billing_NegativeLimit", TestTier2_Billing_NegativeLimitQuery)
	t.Run("Billing_UnsupportedFormat", TestTier2_Billing_UnsupportedExportFormat)
	t.Run("Billing_NonExistentUser", TestTier2_Billing_NonExistentUserUsageQuery)
	t.Run("Billing_MalformedFormat", TestTier2_Billing_MalformedQueryFormatParameter)
}

func RunTier3Tests(t *testing.T) {
	t.Run("Job_Container_Billing", TestTier3_JobSubmission_ContainerLaunch_BillingTracking)
	t.Run("Node_Job_Billing", TestTier3_NodeDrain_JobRequeue_BillingAccounting)
	t.Run("Container_JWT_Recycle_Billing", TestTier3_ContainerJWTValidation_Recycle_BillingPersistence)
	t.Run("MultiJob_Node_Export", TestTier3_MultiJob_NodeToggle_BillingExport)
	t.Run("Quota_Cancel_Recovery", TestTier3_QuotaLimit_JobCancel_ResourceRecovery)
}

func RunTier4Tests(t *testing.T) {
	t.Run("MultiNodeBatchSubmission", TestTier4_MultiNodeBatchSubmissionScenario)
	t.Run("WorkspaceDynamicRecycling", TestTier4_WorkspaceDynamicRecyclingHighLoadScenario)
	t.Run("FullBillingAuditExport", TestTier4_FullBillingReportExportAuditScenario)
}
