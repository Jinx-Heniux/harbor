// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package scan

import (
	"context"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/event/metadata"
	"github.com/goharbor/harbor/src/controller/robot"
	"github.com/goharbor/harbor/src/jobservice/job"
	"github.com/goharbor/harbor/src/lib/log"
	"github.com/goharbor/harbor/src/pkg/notification"
	"github.com/goharbor/harbor/src/pkg/scan"
	v1 "github.com/goharbor/harbor/src/pkg/scan/rest/v1"
	"github.com/goharbor/harbor/src/pkg/scheduler"
	"github.com/goharbor/harbor/src/pkg/task"
)

const (
	// ScanAllCallback the scheduler callback name of the scan all
	ScanAllCallback = "scanAll"
)

func init() {
	if err := scheduler.RegisterCallbackFunc(ScanAllCallback, scanAllCallback); err != nil {
		log.Fatalf("failed to register the callback for the scan all schedule, error %v", err)
	}

	// NOTE: the vendor type of execution for the scan job trigger by the scan all is job.ImageScanAllJob
	if err := task.RegisterCheckInProcessor(job.ImageScanAllJob, scanTaskCheckInProcessor); err != nil {
		log.Fatalf("failed to register the checkin processor for the scan all job, error %v", err)
	}

	if err := task.RegisterCheckInProcessor(job.ImageScanJob, scanTaskCheckInProcessor); err != nil {
		log.Fatalf("failed to register the checkin processor for the scan job, error %v", err)
	}

	if err := task.RegisterTaskStatusChangePostFunc(job.ImageScanJob, scanTaskStatusChange); err != nil {
		log.Fatalf("failed to register the task status change post for the scan job, error %v", err)
	}
}

func scanAllCallback(ctx context.Context, param string) error {
	_, err := DefaultController.ScanAll(ctx, task.ExecutionTriggerSchedule, true)
	return err
}

func scanTaskStatusChange(ctx context.Context, taskID int64, status string) (err error) {
	logger := log.G(ctx).WithFields(log.Fields{"task_id": taskID, "status": status})

	js := job.Status(status)

	if js.Final() {
		t, err := task.Mgr.Get(ctx, taskID)
		if err != nil {
			return err
		}

		if js == job.SuccessStatus {
			robotID := getRobotID(t.ExtraAttrs)
			if robotID > 0 {
				if err := robot.Ctl.Delete(ctx, robotID); err != nil {
					// Should not block the main flow, just logged
					logger.WithFields(log.Fields{"robot_id": robotID, "error": err}).Error("delete robot account failed")
				} else {
					logger.WithField("robot_id", robotID).Debug("Robot account for the scan task is removed")
				}
			}
		}

		artifactID := getArtifactID(t.ExtraAttrs)
		if artifactID > 0 {
			art, err := artifact.Ctl.Get(ctx, artifactID, nil)
			if err != nil {
				logger.WithFields(log.Fields{"artifact_id": artifactID, "error": err}).Errorf("failed to get artifact")
			} else {
				e := &metadata.ScanImageMetaData{
					Artifact: &v1.Artifact{
						NamespaceID: art.ProjectID,
						Repository:  art.RepositoryName,
						Digest:      art.Digest,
						MimeType:    art.ManifestMediaType,
					},
					Status: status,
				}
				// fire event
				notification.AddEvent(ctx, e)
			}
		}

	}

	return nil
}

// scanTaskCheckInProcessor checkin processor handles the webhook of scan job
func scanTaskCheckInProcessor(ctx context.Context, t *task.Task, data string) (err error) {
	checkInReport := &scan.CheckInReport{}
	if err := checkInReport.FromJSON(data); err != nil {
		log.G(ctx).WithField("error", err).Errorf("failed to convert data to report")
		return err
	}

	return DefaultController.UpdateReport(ctx, checkInReport)
}
