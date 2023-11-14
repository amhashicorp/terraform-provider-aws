// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package neptune

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/endpoints"
	"github.com/aws/aws-sdk-go/service/neptune"
	"github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/retry"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/errs/sdkdiag"
	"github.com/hashicorp/terraform-provider-aws/internal/flex"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
	"github.com/hashicorp/terraform-provider-aws/internal/tfresource"
	"github.com/hashicorp/terraform-provider-aws/internal/verify"
	"github.com/hashicorp/terraform-provider-aws/names"
)

// @SDKResource("aws_neptune_cluster_endpoint", name="Cluster Endpoint")
// @Tags(identifierAttribute="arn")
func ResourceClusterEndpoint() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceClusterEndpointCreate,
		ReadWithoutTimeout:   resourceClusterEndpointRead,
		UpdateWithoutTimeout: resourceClusterEndpointUpdate,
		DeleteWithoutTimeout: resourceClusterEndpointDelete,

		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"cluster_endpoint_identifier": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validIdentifier,
			},
			"cluster_identifier": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validIdentifier,
			},
			"endpoint": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"endpoint_type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice(clusterEndpointType_Values(), false),
			},
			"excluded_members": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"static_members": {
				Type:     schema.TypeSet,
				Optional: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			names.AttrTags:    tftags.TagsSchema(),
			names.AttrTagsAll: tftags.TagsSchemaComputed(),
		},

		CustomizeDiff: verify.SetTagsDiff,
	}
}

func resourceClusterEndpointCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).NeptuneConn(ctx)

	input := &neptune.CreateDBClusterEndpointInput{
		DBClusterEndpointIdentifier: aws.String(d.Get("cluster_endpoint_identifier").(string)),
		DBClusterIdentifier:         aws.String(d.Get("cluster_identifier").(string)),
		EndpointType:                aws.String(d.Get("endpoint_type").(string)),
		Tags:                        getTagsIn(ctx),
	}

	if v, ok := d.GetOk("excluded_members"); ok && v.(*schema.Set).Len() > 0 {
		input.ExcludedMembers = flex.ExpandStringSet(v.(*schema.Set))
	}

	if v, ok := d.GetOk("static_members"); ok && v.(*schema.Set).Len() > 0 {
		input.StaticMembers = flex.ExpandStringSet(v.(*schema.Set))
	}

	// Tags are currently only supported in AWS Commercial.
	if meta.(*conns.AWSClient).Partition != endpoints.AwsPartitionID {
		input.Tags = nil
	}

	output, err := conn.CreateDBClusterEndpointWithContext(ctx, input)

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "creating Neptune Cluster Endpoint: %s", err)
	}

	clusterID, clusterEndpointID := aws.StringValue(output.DBClusterIdentifier), aws.StringValue(output.DBClusterEndpointIdentifier)
	d.SetId(clusterEndpointCreateResourceID(clusterID, clusterEndpointID))

	_, err = WaitDBClusterEndpointAvailable(ctx, conn, d.Id())
	if err != nil {
		return sdkdiag.AppendErrorf(diags, "waiting for Neptune Cluster Endpoint (%q) to be Available: %s", d.Id(), err)
	}

	return append(diags, resourceClusterEndpointRead(ctx, d, meta)...)
}

func resourceClusterEndpointRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).NeptuneConn(ctx)

	clusterID, clusterEndpointID, err := clusterEndpointParseResourceID(d.Id())
	if err != nil {
		return sdkdiag.AppendFromErr(diags, err)
	}

	ep, err := FindClusterEndpointByTwoPartKey(ctx, conn, clusterID, clusterEndpointID)

	if !d.IsNewResource() && tfresource.NotFound(err) {
		log.Printf("[WARN] Neptune Cluster Endpoint (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "reading Neptune Cluster Endpoint (%s): %s", d.Id(), err)
	}

	d.Set("arn", ep.DBClusterEndpointArn)
	d.Set("cluster_endpoint_identifier", ep.DBClusterEndpointIdentifier)
	d.Set("cluster_identifier", ep.DBClusterIdentifier)
	d.Set("endpoint", ep.Endpoint)
	d.Set("endpoint_type", ep.CustomEndpointType)
	d.Set("excluded_members", aws.StringValueSlice(ep.ExcludedMembers))
	d.Set("static_members", aws.StringValueSlice(ep.StaticMembers))

	return diags
}

func resourceClusterEndpointUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).NeptuneConn(ctx)

	if d.HasChangesExcept("tags", "tags_all") {
		clusterID, clusterEndpointID, err := clusterEndpointParseResourceID(d.Id())
		if err != nil {
			return sdkdiag.AppendFromErr(diags, err)
		}

		input := &neptune.ModifyDBClusterEndpointInput{
			DBClusterEndpointIdentifier: aws.String(clusterEndpointID),
		}

		if d.HasChange("endpoint_type") {
			input.EndpointType = aws.String(d.Get("endpoint_type").(string))
		}

		if d.HasChange("excluded_members") {
			input.ExcludedMembers = flex.ExpandStringSet(d.Get("excluded_members").(*schema.Set))
		}

		if d.HasChange("static_members") {
			input.StaticMembers = flex.ExpandStringSet(d.Get("static_members").(*schema.Set))
		}

		_, err = conn.ModifyDBClusterEndpointWithContext(ctx, input)

		if err != nil {
			return sdkdiag.AppendErrorf(diags, "updating Neptune Cluster Endpoint (%s): %s", d.Id(), err)
		}

		_, err = WaitDBClusterEndpointAvailable(ctx, conn, d.Id())
		if err != nil {
			return sdkdiag.AppendErrorf(diags, "waiting for Neptune Cluster Endpoint (%q) to be Available: %s", d.Id(), err)
		}
	}

	return append(diags, resourceClusterEndpointRead(ctx, d, meta)...)
}

func resourceClusterEndpointDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	var diags diag.Diagnostics
	conn := meta.(*conns.AWSClient).NeptuneConn(ctx)

	clusterID, clusterEndpointID, err := clusterEndpointParseResourceID(d.Id())
	if err != nil {
		return sdkdiag.AppendFromErr(diags, err)
	}

	_, err = conn.DeleteDBClusterEndpointWithContext(ctx, &neptune.DeleteDBClusterEndpointInput{
		DBClusterEndpointIdentifier: aws.String(clusterEndpointID),
	})

	if tfawserr.ErrCodeEquals(err, neptune.ErrCodeDBClusterNotFoundFault, neptune.ErrCodeDBClusterEndpointNotFoundFault) {
		return diags
	}

	if err != nil {
		return sdkdiag.AppendErrorf(diags, "deleting Neptune Cluster Endpoint (%s): %s", d.Id(), err)
	}

	_, err = WaitDBClusterEndpointDeleted(ctx, conn, d.Id())
	if err != nil {
		if tfawserr.ErrCodeEquals(err, neptune.ErrCodeDBClusterEndpointNotFoundFault) {
			return diags
		}
		return sdkdiag.AppendErrorf(diags, "waiting for Neptune Cluster Endpoint (%q) to be Deleted: %s", d.Id(), err)
	}

	return diags
}

const clusterEndpointResourceIDSeparator = ":"

func clusterEndpointCreateResourceID(clusterID, clusterEndpointID string) string {
	parts := []string{clusterID, clusterEndpointID}
	id := strings.Join(parts, clusterEndpointResourceIDSeparator)

	return id
}

func clusterEndpointParseResourceID(id string) (string, string, error) {
	parts := strings.Split(id, clusterEndpointResourceIDSeparator)

	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1], nil
	}

	return "", "", fmt.Errorf("unexpected format for ID (%[1]s), expected CLUSTER-ID%[2]sCLUSTER-ENDPOINT-ID", id, clusterEndpointResourceIDSeparator)
}

func FindEndpointByID(ctx context.Context, conn *neptune.Neptune, id string) (*neptune.DBClusterEndpoint, error) {
	clusterId, endpointId, err := clusterEndpointParseResourceID(id)
	if err != nil {
		return nil, err
	}
	input := &neptune.DescribeDBClusterEndpointsInput{
		DBClusterIdentifier:         aws.String(clusterId),
		DBClusterEndpointIdentifier: aws.String(endpointId),
	}

	output, err := conn.DescribeDBClusterEndpointsWithContext(ctx, input)

	if tfawserr.ErrCodeEquals(err, neptune.ErrCodeDBClusterEndpointNotFoundFault) ||
		tfawserr.ErrCodeEquals(err, neptune.ErrCodeDBClusterNotFoundFault) {
		return nil, &retry.NotFoundError{
			LastError:   err,
			LastRequest: input,
		}
	}

	if err != nil {
		return nil, err
	}

	if output == nil {
		return nil, &retry.NotFoundError{
			Message:     "Empty result",
			LastRequest: input,
		}
	}

	endpoints := output.DBClusterEndpoints
	if len(endpoints) == 0 {
		return nil, &retry.NotFoundError{
			Message:     "Empty result",
			LastRequest: input,
		}
	}

	return endpoints[0], nil
}

func FindClusterEndpointByTwoPartKey(ctx context.Context, conn *neptune.Neptune, clusterID, clusterEndpointID string) (*neptune.DBClusterEndpoint, error) {
	input := &neptune.DescribeDBClusterEndpointsInput{
		DBClusterIdentifier:         aws.String(clusterID),
		DBClusterEndpointIdentifier: aws.String(clusterEndpointID),
	}

	return findClusterEndpoint(ctx, conn, input)
}

func findClusterEndpoint(ctx context.Context, conn *neptune.Neptune, input *neptune.DescribeDBClusterEndpointsInput) (*neptune.DBClusterEndpoint, error) {
	output, err := findClusterEndpoints(ctx, conn, input)

	if err != nil {
		return nil, err
	}

	return tfresource.AssertSinglePtrResult(output)
}

func findClusterEndpoints(ctx context.Context, conn *neptune.Neptune, input *neptune.DescribeDBClusterEndpointsInput) ([]*neptune.DBClusterEndpoint, error) {
	var output []*neptune.DBClusterEndpoint

	err := conn.DescribeDBClusterEndpointsPagesWithContext(ctx, input, func(page *neptune.DescribeDBClusterEndpointsOutput, lastPage bool) bool {
		if page == nil {
			return !lastPage
		}

		for _, v := range page.DBClusterEndpoints {
			if v != nil {
				output = append(output, v)
			}
		}

		return !lastPage
	})

	if tfawserr.ErrCodeEquals(err, neptune.ErrCodeDBClusterNotFoundFault, neptune.ErrCodeDBClusterEndpointNotFoundFault) {
		return nil, &retry.NotFoundError{
			LastError:   err,
			LastRequest: input,
		}
	}

	if err != nil {
		return nil, err
	}

	return output, nil
}

const (
	// DBClusterEndpoint Unknown
	DBClusterEndpointStatusUnknown = "Unknown"
)

// StatusDBClusterEndpoint fetches the DBClusterEndpoint and its Status
func StatusDBClusterEndpoint(ctx context.Context, conn *neptune.Neptune, id string) retry.StateRefreshFunc {
	return func() (interface{}, string, error) {
		output, err := FindEndpointByID(ctx, conn, id)

		if tfresource.NotFound(err) {
			return nil, "", nil
		}

		if err != nil {
			return nil, DBClusterEndpointStatusUnknown, err
		}

		return output, aws.StringValue(output.Status), nil
	}
}

const (
	// Maximum amount of time to wait for an DBClusterEndpoint to return Available
	DBClusterEndpointAvailableTimeout = 10 * time.Minute

	// Maximum amount of time to wait for an DBClusterEndpoint to return Deleted
	DBClusterEndpointDeletedTimeout = 10 * time.Minute
)

// WaitDBClusterEndpointAvailable waits for a DBClusterEndpoint to return Available
func WaitDBClusterEndpointAvailable(ctx context.Context, conn *neptune.Neptune, id string) (*neptune.DBClusterEndpoint, error) {
	stateConf := &retry.StateChangeConf{
		Pending: []string{"creating", "modifying"},
		Target:  []string{"available"},
		Refresh: StatusDBClusterEndpoint(ctx, conn, id),
		Timeout: DBClusterEndpointAvailableTimeout,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)

	if v, ok := outputRaw.(*neptune.DBClusterEndpoint); ok {
		return v, err
	}

	return nil, err
}

// WaitDBClusterEndpointDeleted waits for a DBClusterEndpoint to return Deleted
func WaitDBClusterEndpointDeleted(ctx context.Context, conn *neptune.Neptune, id string) (*neptune.DBClusterEndpoint, error) {
	stateConf := &retry.StateChangeConf{
		Pending: []string{"deleting"},
		Target:  []string{},
		Refresh: StatusDBClusterEndpoint(ctx, conn, id),
		Timeout: DBClusterEndpointDeletedTimeout,
	}

	outputRaw, err := stateConf.WaitForStateContext(ctx)

	if v, ok := outputRaw.(*neptune.DBClusterEndpoint); ok {
		return v, err
	}

	return nil, err
}
